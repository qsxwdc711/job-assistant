package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jobassistant/backend/internal/application"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/llm"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/profile"
	"jobassistant/backend/internal/task"
	"jobassistant/backend/internal/workflow"
)

const syntheticText = "姓名：张三\n电话：13800138000\n邮箱：synthetic@example.com\n住址：北京市海淀区测试路1号\n本科 计算机科学 境内 2027-06-30 毕业\n使用 Go 开发接口，熟悉 PostgreSQL。"

func syntheticDOCX(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for _, file := range []struct{ name, text string }{{"[Content_Types].xml", `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`}, {"word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + syntheticText + `</w:t></w:r></w:p></w:body></w:document>`}} {
		entry, err := writer.Create(file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write([]byte(file.text)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

type s3Fixture struct {
	*fixture
	profiles    *profile.Store
	matches     *matching.Store
	tasks       *task.Store
	worker      *workflow.Worker
	model       *llm.Client
	vip         identity.User
	scope       profile.Scope
	vaultDir    string
	calls       atomic.Int64
	beforeReply func()
	status      int
}

func newS3Fixture(t *testing.T) *s3Fixture {
	t.Helper()
	f := &s3Fixture{fixture: newFixture(t), vaultDir: t.TempDir()}
	f.vip = f.user(t, "s3-vip", identity.RoleVIP, nil)
	f.scope = profile.Scope{ActorID: f.vip.ID}
	vault, err := profile.OpenVault(f.vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := vault.Close(); err != nil {
			t.Error(err)
		}
	})
	f.profiles = profile.NewStore(f.pool, vault)
	f.matches = matching.NewStore(f.pool)
	f.tasks = task.NewStore(f.pool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer isolated-fake-key" {
			t.Error("unexpected model protocol")
			w.WriteHeader(400)
			return
		}
		var input struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			ResponseFormat map[string]string `json:"response_format"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Messages) != 2 || input.ResponseFormat["type"] != "json_object" {
			t.Error("invalid JSON mode request")
			w.WriteHeader(400)
			return
		}
		var data map[string]string
		if json.Unmarshal([]byte(input.Messages[1].Content), &data) != nil {
			t.Error("model input is not bounded JSON")
			return
		}
		for _, value := range []string{"张三", "13800138000", "synthetic@example.com", "北京市海淀区测试路1号"} {
			if strings.Contains(data["resume"], value) {
				t.Errorf("PII reached model: %s", value)
			}
		}
		if f.beforeReply != nil {
			f.beforeReply()
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		var result any = profile.Candidate{Education: "bachelor", EducationRegion: "unknown", Skills: []string{"Go"}, Evidence: []profile.Evidence{{Field: "education", Quote: "本科"}, {Field: "skills", Quote: "使用 Go 开发接口"}}}
		if data["jd"] != "" {
			result = matching.Semantic{Level: "medium", Required: []matching.Citation{{Statement: "资料展示 Go 接口实践", ResumeQuote: "使用 Go 开发接口", JDQuote: "熟悉 Go"}}, Gaps: []matching.Citation{{Statement: "资料中未找到 Redis 使用证据，需确认", JDQuote: "Redis 经验优先"}}}
		}
		content, err := json.Marshal(result)
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": string(content)}, "finish_reason": "stop"}}}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	f.model, err = llm.New(llm.Options{BaseURL: server.URL, Model: "isolated-model", APIKey: "isolated-fake-key", AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.model.Close)
	f.worker = workflow.New(f.tasks, f.profiles, f.matches, f.model, f.model)
	return f
}
func (f *s3Fixture) upload(t *testing.T) profile.Detail {
	t.Helper()
	ctx := context.Background()
	r, err := f.profiles.Upload(ctx, f.scope, "synthetic.docx", syntheticDOCX(t), []string{"张三"}, "test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	f.run(t)
	d, err := f.profiles.Detail(ctx, f.scope, r.ID, "test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if d.Resume.Status != "review" || !strings.Contains(d.RedactedText, "使用 Go") || strings.Contains(d.RedactedText, "张三") {
		t.Fatalf("local extraction/redaction: %#v", d)
	}
	return d
}
func (f *s3Fixture) run(t *testing.T) {
	t.Helper()
	worked, err := f.worker.RunOne(context.Background())
	if err != nil || !worked {
		t.Fatalf("worker claimed=%v error=%v", worked, err)
	}
}
func (f *s3Fixture) confirm(t *testing.T, d profile.Detail) profile.Version {
	t.Helper()
	v, err := f.profiles.Confirm(context.Background(), f.scope, d.Resume.ID, d.Resume.Revision, profile.Candidate{Education: "bachelor", EducationRegion: "domestic", GraduationDate: "2027-06-30", MajorCodes: []string{"computer_science"}, Skills: []string{"Go"}}, true, "test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func (f *s3Fixture) job(t *testing.T) catalog.Job {
	t.Helper()
	doc := evidenceJob()
	doc.MinimumEducation = "bachelor_or_above"
	doc.GraduationRules = []catalog.GraduationRule{{Region: "domestic", Education: "bachelor_master", From: "2026-09-01", Through: "2027-08-31", Evidence: "2027届毕业生"}}
	doc.Variants[0].Requirements = "本科及以上，熟悉 Go"
	doc.Variants[0].Preferred = "Redis 经验优先"
	doc.Variants[0].Major = catalog.MajorRequirement{Kind: "unrestricted", Evidence: "专业不限"}
	doc.Variants[1].Requirements = "硕士及以上，熟悉 Go"
	doc.Variants[1].Preferred = "Redis 经验优先"
	store := publishEvidence(t, f.fixture, doc)
	page, err := store.Jobs(context.Background(), f.vip.ID, catalog.Filter{Limit: 20, IncludeUnknown: true}, time.Now().UTC())
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("job: %v %v", page, err)
	}
	return page.Items[0]
}
func (f *s3Fixture) recommend(t *testing.T, job catalog.Job) []matching.Recommendation {
	t.Helper()
	r, err := f.matches.Generate(context.Background(), f.scope, []string{job.ID}, true, false, true, f.model.Version(), "test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 2 || (r[0].Status != "pending" && r[0].Status != "succeeded") || r[1].Status != "not_applicable" {
		t.Fatalf("per-variant filtering: %#v", r)
	}
	return r
}

func TestS3RealAdapterWorkerLifecycleAndCache(t *testing.T) {
	f := newS3Fixture(t)
	ctx := context.Background()
	d := f.upload(t)
	if f.calls.Load() != 0 {
		t.Fatal("upload called model without consent")
	}
	if _, err := f.profiles.StartParse(ctx, f.scope, d.Resume.ID, d.Resume.Revision, false, true, "test", time.Now()); !errors.Is(err, identity.ErrInvalidInput) {
		t.Fatalf("parse without consent: %v", err)
	}
	if _, err := f.profiles.StartParse(ctx, f.scope, d.Resume.ID, d.Resume.Revision, true, true, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	d, err := f.profiles.Detail(ctx, f.scope, d.Resume.ID, "test", time.Now())
	if err != nil || d.Resume.Status != "draft" || len(d.Draft.Evidence) != 2 {
		t.Fatalf("structured draft: %v %v", d, err)
	}
	if _, err = f.matches.Generate(ctx, f.scope, []string{"00000000-0000-0000-0000-000000000000"}, true, false, true, f.model.Version(), "test", time.Now()); !errors.Is(err, profile.ErrStale) {
		t.Fatalf("draft participated in matching: %v", err)
	}
	v := f.confirm(t, d)
	job := f.job(t)
	recs := f.recommend(t, job)
	f.run(t)
	results, err := f.matches.List(ctx, f.scope, "", 20, 0, "test", time.Now(), f.model.Version())
	if err != nil {
		t.Fatal(err)
	}
	var succeeded bool
	for _, r := range results {
		if r.VariantKey == "backend" {
			succeeded = r.Status == "succeeded" && r.Semantic.Level == "medium" && r.Eligibility.Status == "eligible"
		}
	}
	if !succeeded || f.calls.Load() != 2 {
		t.Fatalf("semantic/call count: %#v calls=%d", results, f.calls.Load())
	}
	cached := f.recommend(t, job)
	if cached[0].ID != recs[0].ID {
		t.Fatal("cache missed unchanged inputs")
	}
	if worked, err := f.worker.RunOne(ctx); err != nil || worked {
		t.Fatalf("cache repeated model job: %v %v", worked, err)
	}
	apps := application.NewStore(f.pool)
	record, err := apps.Create(ctx, application.Scope{ActorID: f.vip.ID}, application.Input{JobID: job.ID}, "test", time.Now())
	if err != nil || record.ResumeVersionID == nil || *record.ResumeVersionID != v.ID {
		t.Fatalf("application snapshot: %#v %v", record, err)
	}
	d, err = f.profiles.Detail(ctx, f.scope, d.Resume.ID, "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	next := f.confirm(t, d)
	if next.ID == v.ID {
		t.Fatal("confirmation reused mutable profile")
	}
	record, err = apps.Get(ctx, application.Scope{ActorID: f.vip.ID}, record.ID, time.Now())
	if err != nil || record.ResumeVersionID == nil || *record.ResumeVersionID != v.ID {
		t.Fatal("existing application changed when profile changed")
	}
	results, err = f.matches.List(ctx, f.scope, "", 20, 0, "test", time.Now(), f.model.Version())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Status != "stale" {
			t.Fatalf("old result not stale: %s", r.Status)
		}
	}
	other := f.user(t, "other-vip", identity.RoleVIP, nil)
	if _, err = f.profiles.Detail(ctx, profile.Scope{ActorID: other.ID}, d.Resume.ID, "test", time.Now()); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("cross-owner read: %v", err)
	}
	if _, err = f.matches.List(ctx, profile.Scope{ActorID: other.ID}, recs[0].ID, 20, 0, "test", time.Now(), f.model.Version()); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("cross-owner recommendation: %v", err)
	}
	if _, err = f.service.ChangeRole(ctx, f.admin, f.vip.ID, identity.RoleStandard, nil, "revoke"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.profiles.Detail(ctx, f.scope, d.Resume.ID, "test", time.Now()); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("revoked detail: %v", err)
	}
	if _, _, err = f.profiles.Download(ctx, f.scope, d.Resume.ID, "test", time.Now()); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("revoked download: %v", err)
	}
	if list, err := f.profiles.List(ctx, f.scope, 20, 0, "test", time.Now()); err != nil || len(list) != 1 {
		t.Fatalf("ordinary metadata lost: %v", err)
	}
	if err = f.profiles.Delete(ctx, f.scope, d.Resume.ID, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(f.vaultDir)
	if err != nil || len(files) != 0 {
		t.Fatalf("physical deletion: %v %v", files, err)
	}
	record, err = apps.Get(ctx, application.Scope{ActorID: f.vip.ID}, record.ID, time.Now())
	if err != nil || record.ResumeVersionID != nil {
		t.Fatalf("delete destroyed application or retained private FK: %v %v", record, err)
	}
	var sensitive int
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM profile_versions)+(SELECT count(*) FROM recommendations)+(SELECT count(*) FROM resumes WHERE redacted_text<>'' OR draft<>'{}'::jsonb OR sensitive_names<>'[]'::jsonb)`).Scan(&sensitive); err != nil || sensitive != 0 {
		t.Fatalf("private deletion incomplete: %d %v", sensitive, err)
	}
}

func TestS3RevocationAndResourceChangesBlockBeforeAndAfterModel(t *testing.T) {
	for _, change := range []string{"revoke_before", "disable_before", "delete_before", "profile_before", "job_before", "revoke_after", "disable_after", "delete_after", "profile_after", "job_after", "admin_revoke_after"} {
		t.Run(change, func(t *testing.T) {
			f := newS3Fixture(t)
			ctx := context.Background()
			d := f.upload(t)
			f.confirm(t, d)
			job := f.job(t)
			if strings.HasPrefix(change, "admin_") {
				f.scope = profile.Scope{ActorID: f.admin.ID, OwnerID: f.vip.ID, Admin: true}
			}
			recs := f.recommend(t, job)
			mutate := func() {
				var err error
				switch {
				case strings.HasPrefix(change, "admin_"):
					// Keep another administrator active so the original initiating admin can be revoked.
					f.user(t, "another-admin", identity.RoleAdmin, nil)
					_, err = f.service.ChangeRole(ctx, f.admin, f.admin.ID, identity.RoleStandard, nil, "revoke")
				case strings.HasPrefix(change, "revoke"):
					_, err = f.service.ChangeRole(ctx, f.admin, f.vip.ID, identity.RoleStandard, nil, "revoke")
				case strings.HasPrefix(change, "disable"):
					_, err = f.service.SetDisabled(ctx, f.admin, f.vip.ID, true, "disable")
				case strings.HasPrefix(change, "delete"):
					err = f.profiles.Delete(ctx, f.scope, d.Resume.ID, "test", time.Now())
				case strings.HasPrefix(change, "profile"):
					fresh, e := f.profiles.Detail(ctx, f.scope, d.Resume.ID, "test", time.Now())
					if e != nil {
						t.Error(e)
						return
					}
					_, err = f.profiles.Confirm(ctx, f.scope, d.Resume.ID, fresh.Resume.Revision, profile.Candidate{Education: "master"}, true, "test", time.Now())
				case strings.HasPrefix(change, "job"):
					_, err = f.pool.Exec(ctx, `UPDATE jobs SET version=version+1 WHERE id=$1`, job.ID)
				}
				if err != nil {
					t.Error(err)
				}
			}
			after := strings.HasSuffix(change, "after")
			if after {
				f.beforeReply = mutate
			} else {
				mutate()
			}
			worked, err := f.worker.RunOne(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if after && !worked {
				t.Fatal("expected queued work")
			}
			expected := int64(0)
			if after {
				expected = 1
			}
			if f.calls.Load() != expected {
				t.Fatalf("external calls=%d want=%d", f.calls.Load(), expected)
			}
			var published int
			if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM recommendations WHERE id=$1 AND (semantic_status='succeeded' OR semantic<>'{}'::jsonb)`, recs[0].ID).Scan(&published); err != nil || published != 0 {
				t.Fatalf("stale output published: %d %v", published, err)
			}
			var state string
			if err = f.pool.QueryRow(ctx, `SELECT state FROM private_tasks WHERE id=$1`, *recs[0].TaskID).Scan(&state); err != nil || state != "cancelled" {
				t.Fatalf("task state=%s err=%v", state, err)
			}
		})
	}
}

func TestS3LeaseRecoveryAndBoundedRetry(t *testing.T) {
	f := newS3Fixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	r, err := f.profiles.Upload(ctx, f.scope, "lease.docx", syntheticDOCX(t), nil, "test", now)
	if err != nil {
		t.Fatal(err)
	}
	old, err := f.tasks.Claim(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.tasks.Claim(ctx, now); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("same task claimed concurrently: %v", err)
	}
	later := now.Add(121 * time.Second)
	fresh, err := f.tasks.Claim(ctx, later)
	if err != nil || fresh.ID != old.ID || fresh.LeaseToken == old.LeaseToken || fresh.Attempts != 2 {
		t.Fatalf("lease recovery: %v %v", fresh, err)
	}
	if err = f.profiles.Complete(ctx, old, "stale Go resume", profile.Candidate{}, later); !errors.Is(err, task.ErrCancelled) {
		t.Fatalf("expired worker wrote output: %v", err)
	}
	if err = f.tasks.Finish(ctx, old, "", false, false, later); err != nil {
		t.Fatal(err)
	}
	if err = f.profiles.Complete(ctx, fresh, "使用 Go 开发接口", profile.Candidate{}, later); err != nil {
		t.Fatal(err)
	}
	if err = f.tasks.Finish(ctx, fresh, "", false, false, later); err != nil {
		t.Fatal(err)
	}
	d, err := f.profiles.Detail(ctx, f.scope, r.ID, "test", later)
	if err != nil {
		t.Fatal(err)
	}
	f.confirm(t, d)
	job := f.job(t)
	recs := f.recommend(t, job)
	f.status = 429
	for i := 0; i < 3; i++ {
		f.run(t)
		if _, err = f.pool.Exec(ctx, `UPDATE private_tasks SET available_at=now()-interval '1 second' WHERE state='queued'`); err != nil {
			t.Fatal(err)
		}
	}
	if f.calls.Load() != 3 {
		t.Fatalf("retry calls=%d", f.calls.Load())
	}
	var state string
	var attempts int
	if err = f.pool.QueryRow(ctx, `SELECT state,attempts FROM private_tasks WHERE id=$1`, *recs[0].TaskID).Scan(&state, &attempts); err != nil || state != "failed" || attempts != 3 {
		t.Fatalf("retry not bounded: %s/%d %v", state, attempts, err)
	}
	result, err := f.matches.List(ctx, f.scope, recs[0].ID, 20, 0, "test", time.Now(), f.model.Version())
	if err != nil || len(result) != 1 || result[0].Status != "failed" || result[0].Eligibility.Status != "eligible" {
		t.Fatalf("rules lost after model outage: %v %v", result, err)
	}
	if worked, err := f.worker.RunOne(ctx); err != nil || worked {
		t.Fatalf("terminal task was retried: %v %v", worked, err)
	}
}

func TestS3NoKeyAndPreviewConsent(t *testing.T) {
	f := newS3Fixture(t)
	ctx := context.Background()
	d := f.upload(t)
	if _, err := f.profiles.StartParse(ctx, f.scope, d.Resume.ID, d.Resume.Revision, true, false, "test", time.Now()); !errors.Is(err, profile.ErrUnconfigured) {
		t.Fatalf("no-key parse: %v", err)
	}
	if err := f.profiles.UpdatePreview(ctx, f.scope, d.Resume.ID, d.Resume.Revision, d.RedactedText+"\n已移除项目名称", "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.profiles.Confirm(ctx, f.scope, d.Resume.ID, d.Resume.Revision, profile.Candidate{}, true, "test", time.Now()); !errors.Is(err, profile.ErrStale) {
		t.Fatalf("stale preview confirmed: %v", err)
	}
	d, err := f.profiles.Detail(ctx, f.scope, d.Resume.ID, "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.profiles.Confirm(ctx, f.scope, d.Resume.ID, d.Resume.Revision, profile.Candidate{Education: "bachelor"}, false, "test", time.Now())
	if err != nil || v.CloudApproved {
		t.Fatalf("manual confirmation: %v", err)
	}
	job := f.job(t)
	for _, configured := range []bool{false, true} {
		r, err := f.matches.Generate(ctx, f.scope, []string{job.ID}, true, false, configured, fmt.Sprint(configured), "test", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		want := "consent_required"
		if !configured {
			want = "not_configured"
		}
		if r[0].Status != want || r[0].TaskID != nil {
			t.Fatalf("unexpected cloud work without consent/key: %#v", r[0])
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("external call without consent/key")
	}
}

func TestS3ParsePermissionAndExpiryRecheckedAfterCall(t *testing.T) {
	for _, change := range []string{"revoke_before", "revoke_after", "expire_after", "delete_after"} {
		t.Run(change, func(t *testing.T) {
			f := newS3Fixture(t)
			ctx := context.Background()
			d := f.upload(t)
			j, err := f.profiles.StartParse(ctx, f.scope, d.Resume.ID, d.Resume.Revision, true, true, "test", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			mutate := func() {
				var err error
				switch change {
				case "revoke_before", "revoke_after":
					_, err = f.service.ChangeRole(ctx, f.admin, f.vip.ID, identity.RoleStandard, nil, "revoke")
				case "expire_after":
					_, err = f.pool.Exec(ctx, `UPDATE users SET vip_expires_at=now()-interval '1 second' WHERE id=$1`, f.vip.ID)
				case "delete_after":
					err = f.profiles.Delete(ctx, f.scope, d.Resume.ID, "test", time.Now())
				}
				if err != nil {
					t.Error(err)
				}
			}
			if change == "revoke_before" {
				mutate()
			} else {
				f.beforeReply = mutate
			}
			f.run(t)
			var state string
			var raw string
			if err = f.pool.QueryRow(ctx, `SELECT t.state,r.draft::text FROM private_tasks t JOIN resumes r ON r.id=t.resource_id WHERE t.id=$1`, j.ID).Scan(&state, &raw); err != nil || state != "cancelled" {
				t.Fatalf("revoked parse published draft: %s %s %v", state, raw, err)
			}
			var output profile.Candidate
			if err = json.Unmarshal([]byte(raw), &output); err != nil || output.Education != "" || len(output.Skills) > 0 || len(output.Evidence) > 0 {
				t.Fatalf("revoked parse replaced draft: %s %v", raw, err)
			}
			want := int64(1)
			if change == "revoke_before" {
				want = 0
			}
			if f.calls.Load() != want {
				t.Fatalf("calls=%d want=%d", f.calls.Load(), want)
			}
		})
	}
}

func TestS3CrashAfterFinalPublicationDoesNotRepeatModel(t *testing.T) {
	f := newS3Fixture(t)
	ctx := context.Background()
	d := f.upload(t)
	f.confirm(t, d)
	job := f.job(t)
	recs := f.recommend(t, job)
	j, err := f.tasks.Claim(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	input, err := f.matches.Prepare(ctx, j, f.model.Version(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.model.Match(ctx, input.ResumeText, input.JD)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.matches.Complete(ctx, j, f.model.Version(), result, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after publishing on the last allowed attempt but before finishing the queue row.
	if _, err = f.pool.Exec(ctx, `UPDATE private_tasks SET attempts=max_attempts,lease_until=now()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	var state string
	var attempts int
	if err = f.pool.QueryRow(ctx, `SELECT state,attempts FROM private_tasks WHERE id=$1`, j.ID).Scan(&state, &attempts); err != nil || state != "succeeded" || attempts != 4 || f.calls.Load() != 1 {
		t.Fatalf("crash recovery state=%s attempts=%d calls=%d error=%v", state, attempts, f.calls.Load(), err)
	}
	r, err := f.matches.List(ctx, f.scope, recs[0].ID, 20, 0, "test", time.Now(), f.model.Version())
	if err != nil || r[0].Status != "succeeded" {
		t.Fatalf("published result lost: %v %v", r, err)
	}
}

func TestS3PhysicalDeletionOutboxRetriesAndNoTextSurvives(t *testing.T) {
	f := newS3Fixture(t)
	ctx := context.Background()
	d := f.upload(t)
	f.confirm(t, d)
	var key string
	if err := f.pool.QueryRow(ctx, `SELECT storage_key FROM resumes WHERE id=$1`, d.Resume.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(f.vaultDir, key)
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filename, 0700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(filename, "temporary-failure")
	if err := os.WriteFile(blocker, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.profiles.Delete(ctx, f.scope, d.Resume.ID, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM private_file_deletions`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("deletion retry missing: %d %v", pending, err)
	}
	if _, err := f.profiles.Detail(ctx, f.scope, d.Resume.ID, "test", time.Now()); !errors.Is(err, identity.ErrNotFound) {
		t.Fatal("pending physical deletion exposed private text")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := f.profiles.CleanupOne(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filename); !os.IsNotExist(err) {
		t.Fatalf("retry did not remove storage entry: %v", err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM private_file_deletions`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("successful cleanup not acknowledged: %d %v", pending, err)
	}
}
