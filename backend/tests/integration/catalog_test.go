package integration

import (
	"context"
	"testing"
	"time"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
)

func evidenceJob() catalog.Document {
	return catalog.Document{ExternalID: "1282707383965295616", CompanyCode: "tencent", SourceCode: "tencent-campus", CampaignKey: "2027", CampaignName: "2027校园招聘", Title: "后台开发", RecruitmentType: "campus", PublicStatus: "unknown", WrittenTest: "unknown", SourceURL: "https://join.qq.com/post_detail.html?postid=1282707383965295616", ApplyURL: "https://join.qq.com/post_detail.html?postid=1282707383965295616", ApplyLevel: "job", LoginRequired: true, StudentRequired: "unknown", MinimumEducation: "unknown", Variants: []catalog.Variant{
		{Key: "backend", Name: "后端方向", Direction: "backend", Requirements: "计算机相关专业", Major: catalog.MajorRequirement{Kind: "related", Tags: []string{"computer_science"}}, Locations: []catalog.Location{{Code: "shenzhen", Kind: "domestic", Country: "中国", Province: "广东省", City: "深圳市", Raw: "深圳总部"}}},
		{Key: "data", Name: "数据方向", Direction: "data", Major: catalog.MajorRequirement{Kind: "unknown", Tags: []string{}}, Locations: []catalog.Location{{Code: "hefei", Kind: "domestic", Country: "中国", Province: "安徽省", City: "合肥市", Raw: "合肥"}}},
	}}
}

func publishEvidence(t *testing.T, f *fixture, documents ...catalog.Document) *catalog.Store {
	t.Helper()
	store := catalog.NewStore(f.pool)
	now := time.Now().UTC()
	run, err := store.BeginRun(context.Background(), "tencent-campus", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(context.Background(), run, documents, len(documents), nil, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestCatalogVersionIdentityAndCoherentVariantFilters(t *testing.T) {
	f := newFixture(t)
	doc := evidenceJob()
	store := publishEvidence(t, f, doc)
	ctx := context.Background()
	now := time.Now().UTC()
	filter := catalog.Filter{Limit: 20, IncludeUnknown: true}
	page, err := store.Jobs(ctx, f.admin.ID, filter, now)
	if err != nil || page.Total != 1 || page.Items[0].ExternalID != doc.ExternalID {
		t.Fatalf("identity: %#v %v", page, err)
	}
	id := page.Items[0].ID
	publishEvidence(t, f, doc)
	job, err := store.Job(ctx, f.admin.ID, id, now)
	if err != nil || job.Version != 1 {
		t.Fatalf("repeat version: %#v %v", job, err)
	}
	filter.Directions = []string{"backend"}
	filter.Cities = []string{"合肥市"}
	page, err = store.Jobs(ctx, f.admin.ID, filter, now)
	if err != nil || page.Total != 0 {
		t.Fatalf("mixed direction/city admitted: %#v %v", page, err)
	}
	filter.Cities = []string{"深圳市"}
	page, err = store.Jobs(ctx, f.admin.ID, filter, now)
	if err != nil || page.Total != 1 {
		t.Fatalf("coherent direction missing: %#v %v", page, err)
	}
	doc.DeadlineDate = "2026-12-31"
	publishEvidence(t, f, doc)
	job, _ = store.Job(ctx, f.admin.ID, id, now)
	if job.Version != 2 {
		t.Fatalf("deadline version: %d", job.Version)
	}
	doc.CampaignKey = "2028"
	doc.CampaignName = "新批次"
	publishEvidence(t, f, doc)
	page, _ = store.Jobs(ctx, f.admin.ID, catalog.Filter{Limit: 20}, now)
	if page.Total != 2 {
		t.Fatalf("different campaign merged: %d", page.Total)
	}
}

func TestCatalogFailedAndEmptyRunsDoNotCloseJobs(t *testing.T) {
	f := newFixture(t)
	store := publishEvidence(t, f, evidenceJob())
	ctx := context.Background()
	now := time.Now().UTC()
	run, err := store.BeginRun(ctx, "tencent-campus", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailRun(ctx, run, "invalid_response", false, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	page, err := store.Jobs(ctx, f.admin.ID, catalog.Filter{Limit: 20}, now)
	if err != nil || page.Total != 1 || page.Items[0].PublicStatus != "unknown" || page.Items[0].VerificationStatus != "stale" {
		t.Fatalf("failed run changed public state: %#v %v", page, err)
	}
	publishEvidence(t, f)
	page, _ = store.Jobs(ctx, f.admin.ID, catalog.Filter{Limit: 20}, now)
	if page.Total != 1 {
		t.Fatal("successful empty run removed job")
	}
}

func TestCatalogCurrentPermissionAndHiddenPublication(t *testing.T) {
	f := newFixture(t)
	store := publishEvidence(t, f, evidenceJob())
	ctx := context.Background()
	now := time.Now().UTC()
	ordinary, err := f.service.CreateUser(ctx, f.admin, identity.CreateUserInput{Username: "catalog-user", Password: testPassword, Role: identity.RoleStandard}, "test")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := store.Jobs(ctx, f.admin.ID, catalog.Filter{Limit: 20}, now)
	id := page.Items[0].ID
	if err := store.SetPublished(ctx, ordinary.ID, id, false, "核验", "test", now); err != identity.ErrForbidden {
		t.Fatalf("ordinary maintenance: %v", err)
	}
	if err := store.SetPublished(ctx, f.admin.ID, id, false, "等待复核", "test", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Job(ctx, ordinary.ID, id, now); err != identity.ErrNotFound {
		t.Fatalf("hidden disclosure: %v", err)
	}
	if _, err := store.Job(ctx, f.admin.ID, id, now); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogObservedScopeExclusionHidesWithoutClosing(t *testing.T) {
	f := newFixture(t)
	store := publishEvidence(t, f, evidenceJob())
	ctx := context.Background()
	now := time.Now().UTC()
	page, err := store.Jobs(ctx, f.admin.ID, catalog.Filter{Limit: 20}, now)
	if err != nil {
		t.Fatal(err)
	}
	id := page.Items[0].ID
	run, err := store.BeginRun(ctx, "tencent-campus", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(ctx, run, nil, 1, nil, now.Add(time.Second), catalog.Exclusion{CampaignKey: "2027", ExternalID: evidenceJob().ExternalID, Reason: "职责需人工复核"}); err != nil {
		t.Fatal(err)
	}
	job, err := store.Job(ctx, f.admin.ID, id, now)
	if err != nil || job.Published || job.PublicStatus != "unknown" || job.VerificationStatus != "needs_review" {
		t.Fatalf("scope review changed official status: %#v %v", job, err)
	}
}

func TestCatalogSupersededAndDisabledRunsCannotPublish(t *testing.T) {
	f := newFixture(t)
	store := catalog.NewStore(f.pool)
	ctx := context.Background()
	now := time.Now().UTC()
	older, err := store.BeginRun(ctx, "tencent-campus", now)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.BeginRun(ctx, "tencent-campus", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(ctx, older, []catalog.Document{evidenceJob()}, 1, nil, now.Add(2*time.Second)); err == nil {
		t.Fatal("superseded run published")
	}
	if err := store.FailRun(ctx, older, "superseded", false, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSourceEnabled(ctx, f.admin.ID, "tencent-campus", false, "test", now); err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(ctx, current, []catalog.Document{evidenceJob()}, 1, nil, now.Add(4*time.Second)); err != identity.ErrForbidden {
		t.Fatalf("disabled publication: %v", err)
	}
	page, err := store.Jobs(ctx, f.admin.ID, catalog.Filter{Limit: 20}, now)
	if err != nil || page.Total != 0 {
		t.Fatalf("rejected publication leaked rows: %#v %v", page, err)
	}
}

func TestCatalogMajorPreferenceDoesNotExcludeOtherMajors(t *testing.T) {
	f := newFixture(t)
	doc := evidenceJob()
	doc.Variants = doc.Variants[:1]
	doc.Variants[0].Major.Kind = "preferred"
	store := publishEvidence(t, f, doc)
	page, err := store.Jobs(context.Background(), f.admin.ID, catalog.Filter{Limit: 20, Majors: []string{"mathematics"}}, time.Now().UTC())
	if err != nil || page.Total != 1 {
		t.Fatalf("preferred major treated as exclusion: %#v %v", page, err)
	}
}
