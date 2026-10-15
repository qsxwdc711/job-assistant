package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/profile"
)

func TestS3ConcurrentGenerationDeduplicatesAndIdenticalResumesStayPrivate(t *testing.T) {
	f := newS3Fixture(t)
	ctx := context.Background()
	d := f.upload(t)
	f.confirm(t, d)
	job := f.job(t)
	const requests = 6
	var group sync.WaitGroup
	group.Add(requests)
	results := make(chan []matching.Recommendation, requests)
	failures := make(chan error, requests)
	for i := 0; i < requests; i++ {
		go func() {
			defer group.Done()
			items, err := f.matches.Generate(ctx, f.scope, []string{job.ID}, true, false, true, f.model.Version(), "concurrent-test", time.Now())
			if err != nil {
				failures <- err
				return
			}
			results <- items
		}()
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	id := ""
	for items := range results {
		if len(items) != 2 {
			t.Fatalf("result count=%d", len(items))
		}
		if id == "" {
			id = items[0].ID
		} else if items[0].ID != id {
			t.Fatal("concurrent duplicate cache")
		}
	}
	var queued int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM private_tasks WHERE kind='recommend' AND state='queued'`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("duplicate jobs=%d %v", queued, err)
	}
	other := f.user(t, "same-text-other-owner", identity.RoleVIP, nil)
	otherScope := profile.Scope{ActorID: other.ID}
	r, err := f.profiles.Upload(ctx, otherScope, "same-text.docx", syntheticDOCX(t), []string{"张三"}, "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// The older recommendation runs first, then the second owner's local extraction.
	f.run(t)
	f.run(t)
	detail, err := f.profiles.Detail(ctx, otherScope, r.ID, "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.profiles.Confirm(ctx, otherScope, r.ID, detail.Resume.Revision, profile.Candidate{Education: "bachelor", EducationRegion: "domestic", GraduationDate: "2027-06-30", MajorCodes: []string{"computer_science"}, Skills: []string{"Go"}}, true, "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	items, err := f.matches.Generate(ctx, otherScope, []string{job.ID}, true, false, true, f.model.Version(), "test", time.Now())
	if err != nil || items[0].ID == id || items[0].ProfileID != v.ID || items[0].TaskID == nil {
		t.Fatalf("cross-owner cache reused: %v %v", items, err)
	}
	f.run(t)
	if f.calls.Load() != 2 {
		t.Fatalf("private comparisons should each run once, calls=%d", f.calls.Load())
	}
}

func TestS3DynamicDeadlineAndModelConfigurationInvalidateResults(t *testing.T) {
	f := newS3Fixture(t)
	ctx := context.Background()
	d := f.upload(t)
	f.confirm(t, d)
	job := f.job(t)
	recs := f.recommend(t, job)
	f.run(t)
	oldConfig, err := f.matches.List(ctx, f.scope, recs[0].ID, 20, 0, "test", time.Now(), "changed-model-config")
	if err != nil || oldConfig[0].Status != "stale" || oldConfig[0].Semantic.Level != "" {
		t.Fatalf("old model output reused: %v %v", oldConfig, err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE jobs SET deadline_at=now()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	closed, err := f.matches.List(ctx, f.scope, recs[0].ID, 20, 0, "test", time.Now(), f.model.Version())
	if err != nil || closed[0].Eligibility.Status != "ineligible" || closed[0].Semantic.Level != "" {
		t.Fatalf("expired result active: %v %v", closed, err)
	}
	generated, err := f.matches.Generate(ctx, f.scope, []string{job.ID}, true, false, true, f.model.Version(), "test", time.Now())
	if err != nil || generated[0].Eligibility.Status != "ineligible" || generated[0].Status != "not_applicable" {
		t.Fatalf("expired cache regenerated: %v %v", generated, err)
	}
	if f.calls.Load() != 1 {
		t.Fatal("closed job caused model request")
	}
}

func TestS3CurrentProfileStaysVisibleBeyondListLimit(t *testing.T) {
	f := newS3Fixture(t)
	ctx := context.Background()
	d := f.upload(t)
	current := f.confirm(t, d)
	if _, err := f.pool.Exec(ctx, `INSERT INTO profile_versions(owner_user_id,resume_id,candidate,redacted_text,cloud_approved,created_at) SELECT $1,$2,'{}','synthetic later version',false,now()+i*interval '1 second' FROM generate_series(1,105) i`, f.vip.ID, d.Resume.ID); err != nil {
		t.Fatal(err)
	}
	versions, err := f.profiles.Versions(ctx, f.scope, "test", time.Now())
	if err != nil || len(versions) != 100 || !versions[0].Current || versions[0].ID != current.ID {
		t.Fatalf("current version omitted at pagination boundary: count=%d error=%v", len(versions), err)
	}
	detail, err := f.profiles.Detail(ctx, f.scope, d.Resume.ID, "test", time.Now())
	if err != nil || len(detail.Versions) != 100 || !detail.Versions[0].Current {
		t.Fatalf("resume detail lost selected older version: %v", err)
	}
}
