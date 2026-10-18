package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/profile"
)

func zjditSamples(t *testing.T) []map[string]any {
	t.Helper()
	values := array(fixture(t, "zjdit/jobs.json"))
	result := make([]map[string]any, 0, len(values))
	for _, raw := range values {
		result = append(result, object(raw))
	}
	return result
}

func zjditFixtureDetail(t *testing.T, id string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/zjdit/detail-" + id + ".html")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestZJDITOfficialDetailAndScopedCampusRequirements(t *testing.T) {
	for _, sample := range zjditSamples(t) {
		id := text(sample["id"])
		if id != "56" && id != "78" && id != "20" {
			continue
		}
		t.Run(id, func(t *testing.T) {
			if err := zjditVerifyDetail([]byte(zjditFixtureDetail(t, id)), sample); err != nil {
				t.Fatalf("actual public detail rejected: %v", err)
			}
			doc, reason, err := zjditDocument(sample)
			if err != nil {
				t.Fatal(err)
			}
			if id == "20" {
				if reason == "" || doc.ExternalID != "" {
					t.Fatal("unscoped three-year condition was ignored for a campus label")
				}
				return
			}
			if reason != "" || doc.RecruitmentType != "campus" || doc.WrittenTest != "unknown" || len(doc.GraduationRules) != 0 || doc.DeadlineAt != nil || doc.ApplyLevel != "job" || !doc.LoginRequired {
				t.Fatalf("campus conditions or uncertainty changed: %#v %s", doc, reason)
			}
			if id == "56" && (!strings.Contains(doc.CampaignEvidence, "社招需具备3年") || strings.Contains(doc.Variants[0].Requirements, "社招需")) {
				t.Fatal("social-only requirement was applied to campus or raw evidence was lost")
			}
			months := 0
			candidate := profile.Candidate{Education: "master", MajorCodes: []string{"computer_science"}, ExperienceMonths: &months}
			job := catalog.Job{Document: doc}
			for _, check := range matching.Evaluate(candidate, job, doc.Variants[0]).Checks {
				if check.Field == "education" && check.Status != "eligible" || check.Field == "experience" && check.Status == "ineligible" {
					t.Fatalf("campus master with zero experience incorrectly rejected: %#v", check)
				}
			}
			candidate.Education = "bachelor"
			if matching.Evaluate(candidate, job, doc.Variants[0]).Status != "ineligible" {
				t.Fatal("explicit master's degree floor was lost")
			}
		})
	}
}

func TestZJDITDetailMustUseMainPanelAndExactFields(t *testing.T) {
	sample := zjditSamples(t)[0]
	id := text(sample["id"])
	html := zjditFixtureDetail(t, id)
	if err := zjditVerifyDetail([]byte(html+`<aside><h3 class="title">其他岗位</h3><p>博士，8年以上</p></aside>`), sample); err != nil {
		t.Fatalf("unrelated sidebar became part of job requirements: %v", err)
	}
	for _, changed := range []string{
		strings.Replace(html, "details.html?id="+id, "details.html?id=99999999", 1),
		strings.Replace(html, text(sample["name"]), "其他岗位", 1),
		strings.Replace(html, "成都市", "北京市", 1),
		strings.Replace(html, "硕士及以上", "本科及以上", 1),
		strings.Replace(html, ">校招<", ">实习<", 1),
		strings.Replace(html, "登录投递", "已结束", 1),
		html + html,
		`<html><title>请登录</title><a>登录投递</a></html>`,
	} {
		if err := zjditVerifyDetail([]byte(changed), sample); !errors.Is(err, ErrSchema) {
			t.Fatalf("changed or foreign detail accepted: %v", err)
		}
	}
}

func TestZJDITJointCampusAndSocialExperienceIsNotSocialOnly(t *testing.T) {
	entry := zjditSamples(t)[1]
	entry["requirement"] = "<p>计算机相关专业硕士及以上学历，校招和社招需具备3年以上C++开发经验。</p>"
	doc, reason, err := zjditDocument(entry)
	if err != nil || reason == "" || doc.ExternalID != "" {
		t.Fatalf("joint campus/social hard requirement was dropped: reason=%q err=%v", reason, err)
	}
}

func TestZJDITPaginationFormAndSnapshotFailure(t *testing.T) {
	for _, mode := range []string{"complete", "empty", "duplicate", "total_changed", "short_page", "null_array", "wrong_class", "business_failure", "restricted", "late_detail_failure"} {
		t.Run(mode, func(t *testing.T) {
			items := make([]map[string]any, 17)
			for i := range items {
				items[i] = map[string]any{"id": i + 1000, "class_id": 1, "name": "市场经理", "city": "成都市", "qualification": "硕士及以上", "labels": []string{"校招"}, "listing_delisting": 1, "is_del": 0}
			}
			if mode == "wrong_class" {
				items[0]["class_id"] = 2
			}
			if mode == "late_detail_failure" {
				items = zjditSamples(t)[:2]
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && r.URL.Path == "/position/index.html" {
					http.SetCookie(w, &http.Cookie{Name: "anonymous_only", Value: "fixture", Path: "/"})
					if _, err := fmt.Fprint(w, `<title>四川中久数创科技有限公司招聘</title>`); err != nil {
						t.Error(err)
					}
					return
				}
				if r.URL.Path == "/Position/details.html" {
					id := r.URL.Query().Get("id")
					body := zjditFixtureDetail(t, id)
					if id == text(items[1]["id"]) {
						body = strings.Replace(body, "登录投递", "已结束", 1)
					}
					if _, err := fmt.Fprint(w, body); err != nil {
						t.Error(err)
					}
					return
				}
				if mode == "restricted" {
					w.WriteHeader(403)
					return
				}
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Method != "POST" || r.Form.Get("limit") != "15" || r.Form.Get("class_id") != "1" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
					t.Error("public form query contract changed")
				}
				if cookie, err := r.Cookie("anonymous_only"); err != nil || cookie.Value != "fixture" {
					t.Error("anonymous page cookie was not retained")
				}
				start := 0
				if r.Form.Get("page") == "2" {
					start = 15
				}
				var jobs any = items[start:min(start+15, len(items))]
				total, code := len(items), "200"
				if mode == "empty" {
					jobs, total = []any{}, 0
				}
				if mode == "null_array" {
					jobs, total = nil, 0
				}
				if mode == "short_page" {
					jobs = items[:14]
				}
				if mode == "duplicate" && start > 0 {
					jobs = items[:2]
				}
				if mode == "total_changed" && start > 0 {
					total++
				}
				if mode == "business_failure" {
					code = "500"
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"code": code, "count": total, "data": jobs}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			reader := NewReader()
			originalClient := reader.Client
			reader.ZJDITBase = server.URL
			docs, total, err := reader.ZJDIT(context.Background())
			if reader.Client != originalClient || reader.Client.Jar != nil {
				t.Fatal("temporary anonymous client escaped its lifetime")
			}
			switch mode {
			case "complete", "empty":
				want := 17
				if mode == "empty" {
					want = 0
				}
				if err != nil || total != want || len(docs) != 0 {
					t.Fatalf("valid public snapshot rejected: total=%d %v", total, err)
				}
			case "restricted":
				if !errors.Is(err, ErrRestricted) || reader.Requests != 2 {
					t.Fatalf("access restriction retried: %d %v", reader.Requests, err)
				}
			default:
				if !errors.Is(err, ErrSchema) || docs != nil {
					t.Fatalf("incomplete snapshot was partially returned: %v", err)
				}
			}
		})
	}
}
