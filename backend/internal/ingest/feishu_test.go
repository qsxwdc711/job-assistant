package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/profile"
)

func feishuFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	return object(fixture(t, "feishu/"+name+".json"))
}

func TestFeishuEmploymentBoundsAndRealAttendance(t *testing.T) {
	for _, tc := range []struct {
		value    string
		minimum  int
		explicit bool
	}{
		{"必须具有3年以上软件开发工作经验", 3, true},
		{"1年后端开发工作经验", 1, true},
		{"3年软件项目经验和1年后端开发工作经验", 1, true},
		{"三年项目经验", 0, false},
		{"零到一年工作经验", 0, true},
		{"不超过1年工作经验", 0, true},
		{"1年以内工作经验", 0, true},
		{"1-3年工作经验", 1, true},
	} {
		if got, explicit := feishuExperience(tc.value); got != tc.minimum || explicit != tc.explicit {
			t.Errorf("%s got %d/%v want %d/%v", tc.value, got, explicit, tc.minimum, tc.explicit)
		}
	}
	site, _ := feishuConfig("bambulab-careers")
	job := cloneFeishu(t, feishuFixture(t, "bambulab-backend-intern"))
	job["requirement"] = "本科及以上，必须具有3年以上软件开发工作经验"
	if _, reason, err := feishuDocument(site, "7036539082735274248", job); err != nil || reason == "" {
		t.Fatal("experienced software role admitted", reason, err)
	}
	firstPage := feishuFixture(t, "bambulab-list-first")
	var real map[string]any
	for _, raw := range array(firstPage["job_post_list"]) {
		item := object(raw)
		if text(item["id"]) == "7694106748185545010" {
			real = item
		}
	}
	if real == nil {
		t.Fatal("real attendance fixture missing")
	}
	doc, reason, err := feishuDocument(site, "7036539082735274248", real)
	if err != nil || reason != "" || doc.DaysPerWeek == nil || *doc.DaysPerWeek != 4 || doc.MinimumInternshipMonths == nil || *doc.MinimumInternshipMonths != 6 {
		t.Fatalf("real hard attendance lost: %#v %s %v", doc, reason, err)
	}
	days := 1
	result := matching.Evaluate(profile.Candidate{Education: "bachelor", DaysPerWeek: &days}, catalog.Job{Document: doc}, doc.Variants[0])
	if result.Status != "ineligible" {
		t.Fatalf("one day attendance not excluded: %#v", result)
	}
}

func TestFeishuWorkExperienceAndMatcherEvidence(t *testing.T) {
	site, _ := feishuConfig("bambulab-careers")
	for _, tc := range []struct {
		name, requirement  string
		excluded           bool
		education, student string
	}{
		{"project_not_employment", "本科及以上，三年软件项目经验。", false, "bachelor_or_above", "unknown"},
		{"student_preferred", "本科及以上，在校生优先。", false, "bachelor_or_above", "unknown"},
		{"hard_master", "硕士在读，计算机专业。", false, "unknown", "yes"},
		{"english_degree", "Master degree required. Computer science major.", false, "unknown", "unknown"},
		{"work_required", "本科及以上，必须具有三年工作经验。", true, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := cloneFeishu(t, feishuFixture(t, "bambulab-backend-intern"))
			job["requirement"] = tc.requirement
			doc, reason, err := feishuDocument(site, "7036539082735274248", job)
			if err != nil || (reason != "") != tc.excluded {
				t.Fatalf("reason=%s err=%v", reason, err)
			}
			if tc.excluded {
				return
			}
			if doc.StudentRequired != tc.student || doc.MinimumEducation != tc.education {
				t.Fatalf("qualification %#v", doc)
			}
			if tc.name == "hard_master" || tc.name == "english_degree" {
				result := matching.Evaluate(profile.Candidate{Education: "bachelor"}, catalog.Job{Document: doc}, doc.Variants[0])
				if result.Status != "ineligible" {
					t.Fatalf("master floor lost in matching: %#v", result)
				}
			}
		})
	}
	job := cloneFeishu(t, feishuFixture(t, "bambulab-frontend"))
	job["recruit_type"] = map[string]any{"id": "101", "name": "正式", "en_name": "Regular", "parent": map[string]any{"name": "社招"}}
	job["requirement"] = "本科及以上，0-1年工作经验。"
	doc, reason, err := feishuDocument(site, "7036539082735274248", job)
	if err != nil || reason != "" || doc.RecruitmentType != "early_career" || strings.Contains(doc.Variants[0].Requirements, "至少1年工作经验") {
		t.Fatalf("zero to one range %#v %s %v", doc, reason, err)
	}
	job["requirement"] = "本科及以上，三年软件项目经验。"
	if _, reason, err = feishuDocument(site, "7036539082735274248", job); err != nil || reason == "" {
		t.Fatalf("project experience alone admits social role: %s %v", reason, err)
	}
	job["requirement"] = "本科及以上，1年工作经验。"
	doc, reason, err = feishuDocument(site, "7036539082735274248", job)
	zero := 0
	if err != nil || reason != "" || matching.Evaluate(profile.Candidate{Education: "bachelor", ExperienceMonths: &zero}, catalog.Job{Document: doc}, doc.Variants[0]).Status != "ineligible" {
		t.Fatalf("one year floor lost: %s %v", reason, err)
	}
}

func cloneFeishu(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := mokaObject(body)
	if err != nil {
		t.Fatal(err)
	}
	return copy
}

func TestFeishuRealQualificationsAndConservativeScope(t *testing.T) {
	site, _ := feishuConfig("bambulab-careers")
	graduate := feishuFixture(t, "bambulab-frontend")
	doc, reason, err := feishuDocument(site, "7036539082735274248", graduate)
	if err != nil || reason != "" || doc.RecruitmentType != "campus" || doc.Variants[0].Direction != "frontend" || doc.MinimumEducation != "bachelor_or_above" || len(doc.GraduationRules) != 0 {
		t.Fatalf("real graduate %#v %s %v", doc, reason, err)
	}
	if doc.SourceURL != "https://bambulab.jobs.feishu.cn/campus/position/7696343742794549547/detail" {
		t.Fatal("official route", doc.SourceURL)
	}
	intern := feishuFixture(t, "bambulab-backend-intern")
	doc, reason, err = feishuDocument(site, "7036539082735274248", intern)
	if err != nil || reason != "" || doc.RecruitmentType != "internship" || doc.Variants[0].Direction != "backend" {
		t.Fatalf("real intern %#v %s %v", doc, reason, err)
	}
	intern["requirement"] = "硕士及以上在读，计算机相关专业；每周至少4天；实习至少6个月；3年工作经验优先。"
	doc, reason, err = feishuDocument(site, "7036539082735274248", intern)
	if err != nil || reason != "" || doc.MinimumEducation != "unknown" || !strings.Contains(doc.Variants[0].Requirements, "硕士及以上") || doc.StudentRequired != "yes" || doc.DaysPerWeek == nil || *doc.DaysPerWeek != 4 || doc.MinimumInternshipMonths == nil || *doc.MinimumInternshipMonths != 6 || !strings.Contains(doc.Variants[0].Preferred, "3年") {
		t.Fatalf("hard vs preferred %#v %s %v", doc, reason, err)
	}
	intern["requirement"] = "本科及以上，必须具备3年工作经验。"
	if _, reason, err = feishuDocument(site, "7036539082735274248", intern); err != nil || reason == "" {
		t.Fatal("experienced intern admitted", reason, err)
	}
	for _, title := range []string{"AI产品经理实习生", "算法项目经理", "数据运营实习生", "广告投放实习生", "机械结构工程师", "硬件测试工程师"} {
		item := cloneFeishu(t, graduate)
		item["title"] = title
		if feishuDirection(item) != "unknown" {
			t.Fatal("nontechnical title admitted", title)
		}
	}
	for title, want := range map[string]string{"ai-infra基础架构-实习生": "operations_sre", "AI前端开发实习生": "frontend", "AI数据管道开发实习生": "data"} {
		item := cloneFeishu(t, graduate)
		item["title"] = title
		if got := feishuDirection(item); got != want {
			t.Errorf("role family %s got %s want %s", title, got, want)
		}
	}
	item := cloneFeishu(t, graduate)
	item["title"] = "后端开发实习生"
	if _, reason, err = feishuDocument(site, "7036539082735274248", item); err != nil || reason == "" {
		t.Fatal("conflicting intern title admitted", reason, err)
	}
	before, _, _ := feishuDocument(site, "7036539082735274248", graduate)
	graduate["publish_time"] = json.Number("1792999999999")
	after, _, _ := feishuDocument(site, "7036539082735274248", graduate)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("transport timestamps changed stable content")
	}
}

func TestFeishuMissingPublicQualificationsStayExcluded(t *testing.T) {
	site, _ := feishuConfig("agibot-campus")
	job := feishuFixture(t, "agibot-empty-intern")
	doc, reason, err := feishuDocument(site, "7212468542670309689", job)
	if err != nil || reason == "" || doc.ExternalID != "" {
		t.Fatalf("undisclosed conditions should be excluded, not assumed: %#v %s %v", doc, reason, err)
	}
	job["requirement"] = []any{"本科及以上"}
	if _, _, err = feishuDocument(site, "7212468542670309689", job); !errors.Is(err, ErrSchema) {
		t.Fatal("malformed field accepted", err)
	}
}

func TestFeishuNontechnicalTitlesStayOutsidePublication(t *testing.T) {
	site, _ := feishuConfig("bambulab-careers")
	for _, title := range []string{"市场营销数据分析实习生", "Security Sales Intern", "Frontend Marketing Intern", "Security Manager Intern"} {
		job := cloneFeishu(t, feishuFixture(t, "bambulab-backend-intern"))
		job["title"] = title
		job["description"] = "负责市场推广与客户沟通"
		job["requirement"] = "本科及以上学历"
		if got := feishuDirection(job); got != "unknown" {
			t.Errorf("nontechnical title %s classified as %s", title, got)
		}
		doc, reason, err := feishuDocument(site, "7036539082735274248", job)
		if err != nil || reason == "" || doc.ExternalID != "" {
			t.Errorf("nontechnical title reaches document: %s %#v %s %v", title, doc, reason, err)
		}
	}
}

func TestFeishuCompleteSnapshotAndLateFailure(t *testing.T) {
	for _, mode := range []string{"complete", "empty", "duplicate", "changed_count", "short_page", "null_jobs", "restricted", "late_detail", "wrong_detail", "closed_detail", "wrong_tenant", "missing_token", "changed_requirements", "final_identity_change", "final_reorder"} {
		t.Run(mode, func(t *testing.T) {
			site, _ := feishuConfig("bambulab-careers")
			jobs := make([]map[string]any, 11)
			for i := range jobs {
				jobs[i] = cloneFeishu(t, feishuFixture(t, "bambulab-frontend"))
				jobs[i]["id"] = "76963437427945495" + fmt.Sprintf("%02d", i)
			}
			if mode == "duplicate" {
				jobs[1]["id"] = jobs[0]["id"]
			}
			var calls, lists atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if _, err := r.Cookie("applicant_private"); err == nil {
					t.Error("reused applicant cookie")
				}
				if r.URL.Path == "/campus/position/list" {
					tenant := "Bambulab"
					if mode == "wrong_tenant" {
						tenant = "Another company"
					}
					http.SetCookie(w, &http.Cookie{Name: "public_session", Value: "fixture", Path: "/"})
					fmt.Fprintf(w, `<script id="js-websiteInfo" type="text/json">{"tenant_info":{"tenant_name":%q},"website_info":{"id":"7036539082735274248","path":"campus"}}</script>`, tenant)
					return
				}
				if _, err := r.Cookie("public_session"); err != nil {
					t.Error("public session lost")
				}
				data := map[string]any{}
				if r.URL.Path == "/api/v1/csrf/token" {
					if mode != "missing_token" {
						data["token"] = "fictional-public-csrf"
					}
				} else {
					if r.Header.Get("X-CSRF-Token") != "fictional-public-csrf" || r.Header.Get("website-path") != "campus" {
						t.Error("normal public CSRF flow lost")
					}
					switch {
					case r.URL.Path == "/api/v1/search/job/posts":
						listRead := lists.Add(1)
						if mode == "restricted" {
							w.WriteHeader(403)
							return
						}
						var input map[string]any
						if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
							t.Error(err)
						}
						offset := int(input["offset"].(float64))
						total := len(jobs)
						if input["portal_type"] != float64(6) || input["limit"] != float64(10) {
							t.Error("portal/limit changed")
						}
						list := []any{}
						for i := offset; i < min(offset+10, len(jobs)); i++ {
							list = append(list, jobs[i])
						}
						if listRead > 2 && mode == "final_reorder" {
							list = []any{}
							for i := offset; i < min(offset+10, len(jobs)); i++ {
								list = append(list, jobs[len(jobs)-i-1])
							}
						}
						if listRead == 3 && mode == "final_identity_change" {
							jobs[0]["id"] = "7696343742794549999"
						}
						if mode == "empty" {
							total = 0
							list = []any{}
						}
						if mode == "changed_count" && offset > 0 {
							total++
						}
						if mode == "short_page" && offset == 0 {
							list = list[:9]
						}
						if mode == "null_jobs" {
							list = nil
						}
						data = map[string]any{"count": total, "job_post_list": list}
					case strings.HasPrefix(r.URL.Path, "/api/v1/job/posts/"):
						id := strings.TrimPrefix(r.URL.Path, "/api/v1/job/posts/")
						index, _ := strconv.Atoi(id[len(id)-2:])
						if mode == "late_detail" && index == 10 {
							w.WriteHeader(403)
							return
						}
						detail := cloneFeishu(t, jobs[index])
						if mode == "wrong_detail" {
							detail["id"] = "7696343742794549999"
						}
						if mode == "closed_detail" {
							detail["channel_online_status"] = json.Number("0")
						}
						if mode == "changed_requirements" {
							detail["requirement"] = "另一个硬门槛"
						}
						data = map[string]any{"job_post_detail": detail}
					default:
						t.Error("unexpected applicant/private endpoint", r.URL.Path)
						w.WriteHeader(404)
						return
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			reader := NewReader()
			reader.FeishuBase = server.URL
			reader.feishuReadSpacing = 200 * time.Millisecond
			privateJar, jarErr := cookiejar.New(nil)
			if jarErr != nil {
				t.Fatal(jarErr)
			}
			serverURL, parseErr := url.Parse(server.URL)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			privateJar.SetCookies(serverURL, []*http.Cookie{{Name: "applicant_private", Value: "test-only", Path: "/"}})
			reader.Client.Jar = privateJar
			original := reader.Client
			docs, total, err := reader.Feishu(context.Background(), site.source)
			if reader.Client != original {
				t.Fatal("public reader client leaked")
			}
			if mode == "complete" || mode == "final_reorder" {
				if err != nil || total != 11 || len(docs) != 11 {
					t.Fatalf("complete %d %d %v", total, len(docs), err)
				}
			} else if mode == "empty" {
				if err != nil || total != 0 || len(docs) != 0 {
					t.Fatal(total, len(docs), err)
				}
			} else {
				if err == nil || docs != nil {
					t.Fatal("partial or invalid snapshot accepted", len(docs), err)
				}
				if (mode == "restricted" || mode == "late_detail") && !errors.Is(err, ErrRestricted) {
					t.Fatal(err)
				}
				if mode == "restricted" && calls.Load() != 3 {
					t.Fatalf("restriction retried %d times", calls.Load())
				}
			}
		})
	}
}
