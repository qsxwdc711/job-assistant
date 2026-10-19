package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/profile"
)

func chaitinSamples(t *testing.T) []map[string]any {
	t.Helper()
	var result []map[string]any
	for _, raw := range array(fixture(t, "chaitin/jobs.json")) {
		result = append(result, object(raw))
	}
	return result
}

func chaitinFixturePage(t *testing.T, id string) string {
	t.Helper()
	body, err := os.ReadFile("testdata/chaitin/detail.html")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(body), "49d66d9f-62a5-4878-a7f7-6f30243498d0", id)
}

func TestChaitinRealTypesAndInternshipEligibility(t *testing.T) {
	samples := chaitinSamples(t)
	brand, err := chaitinBrandFromPage([]byte(chaitinFixturePage(t, text(samples[0]["job_id"]))))
	if err != nil {
		t.Fatal(err)
	}
	if brand.Email != "resume@chaitin.com" {
		t.Fatal("a template fallback replaced the actual brand-configured application address")
	}
	for i, sample := range samples {
		doc, reason, err := chaitinDocument(sample, brand)
		if err != nil {
			t.Fatal(err)
		}
		if i == 5 || i == 8 || i == 9 || i == 10 {
			if reason == "" || doc.ExternalID != "" {
				t.Fatalf("experienced, contradictory, nontechnical or unscoped social role published: %s", text(sample["title"]))
			}
			continue
		}
		if reason != "" || doc.ApplyLevel != "job" || doc.LoginRequired || doc.WrittenTest != "unknown" || doc.DeadlineAt != nil || len(doc.GraduationRules) != 0 || !strings.Contains(doc.CampaignEvidence, text(sample["tracking_code"])) {
			t.Fatalf("type/evidence boundary lost: %#v %s", doc, reason)
		}
		if i == 1 && doc.RecruitmentType != "internship" {
			t.Fatal("a social-site internship was treated as full-time social recruitment")
		}
		if i == 2 || i == 3 {
			if doc.StudentRequired != "yes" || doc.MinimumInternshipMonths == nil || *doc.MinimumInternshipMonths != 6 || doc.DaysPerWeek == nil || *doc.DaysPerWeek != 4 {
				t.Fatalf("actual internship commitments lost: %#v", doc)
			}
			student, months, days := true, 6, 4
			candidate := profile.Candidate{Education: "bachelor", MajorCodes: []string{"computer_science"}, Student: &student, InternshipMonths: &months, DaysPerWeek: &days}
			for _, check := range matching.Evaluate(candidate, catalog.Job{Document: doc}, doc.Variants[0]).Checks {
				if check.Field == "student" || check.Field == "internship_months" || check.Field == "days_per_week" || check.Field == "education" {
					if check.Status != "eligible" {
						t.Fatalf("confirmed candidate rejected: %#v", check)
					}
				}
			}
			months = 3
			if matching.Evaluate(candidate, catalog.Job{Document: doc}, doc.Variants[0]).Status != "ineligible" {
				t.Fatal("insufficient internship months accepted")
			}
			months, days = 6, 3
			if matching.Evaluate(candidate, catalog.Job{Document: doc}, doc.Variants[0]).Status != "ineligible" {
				t.Fatal("insufficient attendance accepted")
			}
			days, student = 4, false
			if matching.Evaluate(candidate, catalog.Job{Document: doc}, doc.Variants[0]).Status != "ineligible" {
				t.Fatal("explicit student requirement ignored")
			}
		}
		if i == 4 && doc.Variants[0].Direction != "operations_sre" {
			t.Fatal("Linux technical support duties were presented as backend development")
		}
		if i == 6 {
			unknown := false
			for _, place := range doc.Variants[0].Locations {
				unknown = unknown || place.Raw == "新疆" && place.Kind == "unknown"
			}
			if !unknown {
				t.Fatal("a province-only location became a fabricated city")
			}
		}
		if i == 11 {
			if result := matching.Evaluate(profile.Candidate{Education: "bachelor"}, catalog.Job{Document: doc}, doc.Variants[0]); result.Status != "ineligible" {
				t.Fatal("the explicit master's/doctorate student requirement admitted a bachelor's candidate")
			}
		}
		if i == 12 || i == 13 {
			zero := 0
			if result := matching.Evaluate(profile.Candidate{Education: "bachelor", ExperienceMonths: &zero}, catalog.Job{Document: doc}, doc.Variants[0]); result.Status != "ineligible" {
				t.Fatal("the explicit one-year employment requirement admitted a zero-experience candidate")
			}
		}
	}
}

func TestChaitinPageIdentityAndApplicationContract(t *testing.T) {
	id := text(chaitinSamples(t)[0]["job_id"])
	page := chaitinFixturePage(t, id)
	brand, err := chaitinBrandFromPage([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	if err := chaitinVerifyPage([]byte(page), id, brand); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{
		strings.ReplaceAll(page, id, "00000000-0000-0000-0000-000000000001"),
		strings.Replace(page, id, "00000000-0000-0000-0000-000000000001", 1),
		strings.ReplaceAll(page, "resume@chaitin.com", "hr@unverified.example"),
		strings.ReplaceAll(page, "{{tracking_code}}", ""),
		page + page,
		`<title>登录邮箱</title>`,
	} {
		if chaitinVerifyPage([]byte(changed), id, brand) == nil {
			t.Fatal("wrong job, changed application configuration or generic template accepted")
		}
	}
}

func TestChaitinPreferencesAndMetadataCannotCreateHardRequirementsOrVersions(t *testing.T) {
	sample := chaitinSamples(t)[0]
	brand, err := chaitinBrandFromPage([]byte(chaitinFixturePage(t, text(sample["job_id"]))))
	if err != nil {
		t.Fatal(err)
	}
	sample["title"], sample["recruitment_type"] = "AI研发工程师", "social"
	sample["description"] = "岗位职责\n开发模型系统。\n任职要求\n接受应届毕业生。\n加分项\n硕士及以上学历。\n3年以上开发经验。"
	doc, reason, err := chaitinDocument(sample, brand)
	if err != nil || reason != "" || doc.RecruitmentType != "early_career" || strings.Contains(doc.Variants[0].Requirements, "硕士") || !strings.Contains(doc.Variants[0].Preferred, "3年以上") {
		t.Fatalf("preferred conditions changed hard eligibility: %#v %s %v", doc, reason, err)
	}
	if matching.Evaluate(profile.Candidate{Education: "bachelor"}, catalog.Job{Document: doc}, doc.Variants[0]).Status == "ineligible" {
		t.Fatal("a bachelor's candidate was rejected by preferred master's qualifications")
	}
	sample["updated_at"] = json.Number("1792400000")
	after, reason, err := chaitinDocument(sample, brand)
	if err != nil || reason != "" || !reflect.DeepEqual(doc, after) {
		t.Fatal("an operational timestamp created a business-content version")
	}
	sample["description"] = "岗位职责\n开发系统。\n任职要求\n要求3年以上工作经验，开源项目经验优先。"
	if doc, reason, err := chaitinDocument(sample, brand); err != nil || reason == "" || doc.ExternalID != "" {
		t.Fatal("a preferred clause erased a separate hard three-year requirement")
	}
}

func TestChaitinSnapshotCompletenessAndLateFailures(t *testing.T) {
	for _, mode := range []string{"complete", "empty", "duplicate", "changed_total", "short_page", "null_items", "wrong_next", "bad_code", "restricted", "late_detail", "late_page"} {
		t.Run(mode, func(t *testing.T) {
			items := make([]map[string]any, 12)
			for i := range items {
				items[i] = map[string]any{}
				for key, value := range chaitinSamples(t)[9] {
					items[i][key] = value
				}
				items[i]["job_id"] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
			}
			if strings.HasPrefix(mode, "late_") {
				items = chaitinSamples(t)[:2]
			}
			basePath := "/plugins/career_site/"
			writeText := func(w http.ResponseWriter, body string) {
				if _, err := fmt.Fprint(w, body); err != nil {
					t.Error(err)
				}
			}
			writeJSON := func(w http.ResponseWriter, value any) {
				if err := json.NewEncoder(w).Encode(value); err != nil {
					t.Error(err)
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == basePath+"sites/default" {
					writeText(w, chaitinFixturePage(t, text(items[0]["job_id"])))
					return
				}
				if strings.HasPrefix(r.URL.Path, basePath+"sites/default/jobs/") {
					id := strings.TrimPrefix(r.URL.Path, basePath+"sites/default/jobs/")
					body := chaitinFixturePage(t, id)
					if mode == "late_page" && id == text(items[1]["job_id"]) {
						body = strings.ReplaceAll(body, id, text(items[0]["job_id"]))
					}
					writeText(w, body)
					return
				}
				if strings.HasPrefix(r.URL.Path, basePath+"api/default/jobs/") {
					id := strings.TrimPrefix(r.URL.Path, basePath+"api/default/jobs/")
					for _, item := range items {
						if text(item["job_id"]) == id {
							detail := item
							if mode == "late_detail" && id == text(items[1]["job_id"]) {
								detail = map[string]any{"job_id": id, "title": "错误详情"}
							}
							writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"job": detail}})
							return
						}
					}
					w.WriteHeader(404)
					return
				}
				if r.Method != "GET" || r.URL.Path != basePath+"api/default/jobs" || r.URL.Query().Get("size") != "10" {
					t.Error("public query contract changed")
				}
				if mode == "restricted" {
					w.WriteHeader(403)
					return
				}
				start := 0
				if r.URL.Query().Get("page") == "2" {
					start = 10
				}
				var page any = items[start:min(start+10, len(items))]
				total, code, next := len(items), 0, start+10 < len(items)
				switch mode {
				case "empty":
					page, total, next = []any{}, 0, false
				case "duplicate":
					if start > 0 {
						page = items[:2]
					}
				case "changed_total":
					if start > 0 {
						total++
					}
				case "short_page":
					page = items[:9]
				case "null_items":
					page, total, next = nil, 0, false
				case "wrong_next":
					next = !next
				case "bad_code":
					code = -1
				}
				writeJSON(w, map[string]any{"code": code, "data": map[string]any{"items": page, "total_count": total, "has_next_page": next}})
			}))
			defer server.Close()
			reader := NewReader()
			reader.chaitinReadSpacing = 0 // Local fixture service has no remote request budget.
			client := reader.Client
			reader.ChaitinBase = server.URL
			docs, total, err := reader.Chaitin(context.Background())
			if client != reader.Client || reader.readSpacing != 0 {
				t.Fatal("temporary source read settings escaped their lifetime")
			}
			switch mode {
			case "complete", "empty":
				want := 12
				if mode == "empty" {
					want = 0
				}
				if err != nil || total != want || len(docs) != 0 {
					t.Fatalf("valid snapshot failed: total=%d docs=%d err=%v", total, len(docs), err)
				}
			case "restricted":
				if !errors.Is(err, ErrRestricted) || reader.Requests != 2 {
					t.Fatalf("restricted source retried: requests=%d err=%v", reader.Requests, err)
				}
			default:
				if !errors.Is(err, ErrSchema) || docs != nil {
					t.Fatalf("incomplete/changed snapshot returned partial documents: %d %v", len(docs), err)
				}
			}
		})
	}
}
