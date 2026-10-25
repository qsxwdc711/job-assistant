package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/profile"
)

func beisenFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	body, err := os.ReadFile("testdata/beisen/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func beisenTestHome(t *testing.T, site beisenSite) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"tenantInfo": map[string]any{"Id": site.tenantID, "Alias": site.companyName, "Status": 1}, "OpenCampusCategoryIds": []string{"2"}})
	if err != nil {
		t.Fatal(err)
	}
	return `<script>var BSGlobal = ` + string(body) + `; BSGlobal.staticPath = "https://example.invalid/";</script>`
}

func beisenTestWrite(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		t.Error(err)
	}
}

func TestBeisenRealCompletePaginationAndQualification(t *testing.T) {
	for _, sample := range []struct{ source, prefix string }{{"miniso-campus", "miniso"}, {"mxbc-campus", "mxbc"}} {
		t.Run(sample.source, func(t *testing.T) {
			site, ok := beisenConfig(sample.source)
			if !ok {
				t.Fatal("verified source is not registered")
			}
			pages := []map[string]any{beisenFixture(t, sample.prefix+"-list-0"), beisenFixture(t, sample.prefix+"-list-1")}
			lists := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/campus/jobs" {
					http.SetCookie(w, &http.Cookie{Name: "anonymous_research", Value: "public-only", Path: "/"})
					fmt.Fprint(w, beisenTestHome(t, site))
					return
				}
				if cookie, err := request.Cookie("anonymous_research"); err != nil || cookie.Value != "public-only" {
					t.Error("isolated anonymous homepage session was lost")
				}
				switch request.URL.Path {
				case "/api/Jobad/GetJobAdPageList":
					if request.Method != http.MethodPost {
						t.Error("wrong list HTTP method")
					}
					var query map[string]any
					if err := json.NewDecoder(request.Body).Decode(&query); err != nil || query["KeyWords"] != "" || query["PageSize"] != float64(20) || query["PortalId"] != "" || !reflect.DeepEqual(query["Category"], []any{"2"}) {
						t.Error("public full-list query changed")
					}
					page := int(query["PageIndex"].(float64))
					if page < 0 || page > 1 {
						t.Error("unbounded pagination")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					lists++
					if lists > 2 {
						// Sort metadata can reorder a stable complete snapshot.
						entries := append(append([]any{}, array(pages[0]["Data"])...), array(pages[1]["Data"])...)
						for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
							entries[left], entries[right] = entries[right], entries[left]
						}
						start := page * 20
						beisenTestWrite(t, w, map[string]any{"Code": 200, "Count": len(entries), "Data": entries[start:min(start+20, len(entries))]})
						return
					}
					beisenTestWrite(t, w, pages[page])
				case "/api/JobAd/GetJobAdInfo":
					query := request.URL.Query()
					if request.Method != http.MethodGet || query.Get("category") != "2" || !strings.Contains(query.Get("displayFields"), "Degree") || !strings.Contains(query.Get("displayFields"), "YearsOfWorking") {
						t.Error("public details omit qualification fields")
					}
					beisenTestWrite(t, w, beisenFixture(t, sample.prefix+"-detail-"+query.Get("jobAdId")))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			reader := NewReader()
			original := reader.Client
			reader.BeisenBase = server.URL
			docs, total, err := reader.Beisen(context.Background(), sample.source)
			if err != nil || len(docs) != 2 || lists != 4 || total != map[string]int{"miniso": 28, "mxbc": 26}[sample.prefix] || reader.Client != original || reader.Client.Jar != nil {
				t.Fatalf("complete source/session: total=%d docs=%d lists=%d err=%v", total, len(docs), lists, err)
			}
			for _, doc := range docs {
				if doc.RecruitmentType != "campus" || !doc.LoginRequired || doc.MinimumEducation != "bachelor_or_above" || doc.WrittenTest != "unknown" || len(doc.GraduationRules) != 0 || doc.Variants[0].Locations[0].Kind != "domestic" || !strings.Contains(doc.SourceURL, doc.ExternalID) {
					t.Fatalf("qualification/source not preserved: %+v", doc)
				}
			}
			if sample.prefix == "mxbc" {
				found := false
				for _, exclusion := range reader.Exclusions {
					found = found || strings.Contains(exclusion.Reason, "届数")
				}
				if !found {
					t.Fatal("2027-title/2026-requirement cohort conflict was not retained")
				}
			}
		})
	}
}

func TestBeisenRejectsIncompleteOrMixedPublicSnapshots(t *testing.T) {
	for _, scenario := range []string{"wrong_tenant", "empty", "short_page", "duplicate", "wrong_category", "invalid_numeric_id", "changed_total", "changed_identity", "changed_status", "changed_content", "restricted", "late_detail_failure", "non_string_qualifications"} {
		t.Run(scenario, func(t *testing.T) {
			site, _ := beisenConfig("miniso-campus")
			response := beisenFixture(t, "miniso-detail-1f528032-29cd-4384-93de-ef66e6c96a9f")
			job := object(response["Data"])
			listReads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/campus/jobs" {
					if scenario == "wrong_tenant" {
						site.tenantID++
					}
					fmt.Fprint(w, beisenTestHome(t, site))
					return
				}
				switch request.URL.Path {
				case "/api/Jobad/GetJobAdPageList":
					listReads++
					jobs, total := []any{job}, 1
					switch scenario {
					case "empty":
						jobs, total = []any{}, 0
					case "short_page":
						total = 2
					case "duplicate":
						jobs, total = []any{job, job}, 2
					case "wrong_category":
						job["CategoryId"] = "1"
					case "invalid_numeric_id":
						job["JobAdId"] = "invalid"
					case "changed_total":
						if listReads > 1 {
							jobs, total = []any{}, 0
						}
					case "changed_content":
						if listReads > 1 {
							job["Require"] = "硕士及以上学历"
						}
					}
					beisenTestWrite(t, w, map[string]any{"Code": 200, "Count": total, "Data": jobs})
				case "/api/JobAd/GetJobAdInfo":
					switch scenario {
					case "changed_identity":
						job["Id"] = "b336d5cd-5a95-45f2-ac23-bc3159c86a42"
					case "changed_status":
						job["Status"] = 2
					case "non_string_qualifications":
						job["Require"] = []any{"本科及以上学历"}
					case "restricted":
						beisenTestWrite(t, w, map[string]any{"Code": 403, "Data": nil})
						return
					case "late_detail_failure":
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					beisenTestWrite(t, w, response)
				}
			}))
			defer server.Close()
			reader := NewReader()
			reader.BeisenBase = server.URL
			docs, _, err := reader.Beisen(context.Background(), "miniso-campus")
			if err == nil || len(docs) != 0 {
				t.Fatalf("partial/untrusted source was accepted: err=%v docs=%d", err, len(docs))
			}
			if scenario == "restricted" && !errors.Is(err, ErrRestricted) {
				t.Fatalf("access restriction lost: %v", err)
			}
		})
	}
}

func TestBeisenHardQualificationsAndStableContent(t *testing.T) {
	site, _ := beisenConfig("miniso-campus")
	job := object(beisenFixture(t, "miniso-detail-1f528032-29cd-4384-93de-ef66e6c96a9f")["Data"])
	original, reason, err := beisenDocument(site, job)
	if err != nil || reason != "" {
		t.Fatalf("actual technical campus detail: %s %v", reason, err)
	}
	job["ChangeDate"], job["PostDateInt"], job["IsCollect"], job["IsDelivered"] = "2099-01-01", 99, true, true
	changed, reason, err := beisenDocument(site, job)
	if err != nil || reason != "" || !reflect.DeepEqual(original, changed) {
		t.Fatal("tracking/session metadata created a new content version")
	}
	job["Degree"] = "硕士及以上"
	doc, reason, err := beisenDocument(site, job)
	if err != nil || reason != "" || !strings.Contains(doc.Variants[0].Requirements, "官方学历字段：硕士及以上") || doc.MinimumEducation == "bachelor_or_above" {
		t.Fatal("higher structured degree floor was lost")
	}
	job["Degree"] = "硕士"
	doc, reason, err = beisenDocument(site, job)
	if err != nil || reason != "" || matching.Evaluate(profile.Candidate{Education: "bachelor"}, catalog.Job{Document: doc}, doc.Variants[0]).Status != "ineligible" {
		t.Fatal("short structured degree name failed to enforce the higher floor")
	}
	job["YearsOfWorking"] = "3年以上工作经验"
	if _, reason, _ := beisenDocument(site, job); reason == "" {
		t.Fatal("structured experience exceeding one year was admitted")
	}
	job["YearsOfWorking"] = nil
	job["Require"] = "本科及以上学历，计算机相关专业；要求3年工作经验；有5年项目经验者优先"
	if _, reason, _ := beisenDocument(site, job); reason == "" {
		t.Fatal("hard prose experience exceeding one year was admitted")
	}
	job["Require"] = "本科及以上学历，计算机相关专业；有3年项目经验者优先"
	doc, reason, err = beisenDocument(site, job)
	if err != nil || reason != "" || !strings.Contains(doc.Variants[0].Preferred, "3年") || strings.Contains(doc.Variants[0].Requirements, "3年") {
		t.Fatal("preferred experience became a hard eligibility condition")
	}
	job["Degree"] = nil
	job["JobAdName"], job["Require"] = "2027届AI全栈创优生", "2026届本科及以上学历；仅2026届可投"
	if _, reason, _ := beisenDocument(site, job); reason == "" {
		t.Fatal("repeated conflicting cohort was admitted")
	}
	job["JobAdName"], job["Duty"] = "市场营销管培生", "使用AI工具整理营销数据和制作PPT"
	if _, reason, _ := beisenDocument(site, job); reason == "" {
		t.Fatal("generic AI marketing was misclassified as technical")
	}
}

func TestBeisenEmploymentExperienceExcludesProjectsAndKeepsZero(t *testing.T) {
	for _, sample := range []struct {
		value string
		want  int
	}{
		{"具备3年软件项目经验", 0},
		{"具备3年软件项目经验和1年工作经验", 1},
		{"要求3年工作经验", 3},
		{"0-1年工作经验", 0},
		{"零至一年工作经验", 0},
		{"1年工作经验", 1},
		{"0-1 years of work experience", 0},
		{"2 years of professional experience", 2},
	} {
		t.Run(sample.value, func(t *testing.T) {
			got, err := beisenExperience(sample.value)
			if err != nil || got != sample.want {
				t.Fatalf("minimum employment experience: got=%d want=%d err=%v", got, sample.want, err)
			}
		})
	}
}

func TestBeisenSectionPreferencesAndEmploymentMatching(t *testing.T) {
	site, _ := beisenConfig("miniso-campus")
	for _, sample := range []struct {
		name, requirements, field string
		wantYear, wantMajorBonus  bool
	}{
		{name: "project", requirements: "本科及以上学历，计算机相关专业；具备3年软件项目经验"},
		{name: "bonus_section", requirements: "任职要求：\n本科及以上学历，计算机相关专业\n加分项：\n3年工作经验\n硕士学历"},
		{name: "bonus_major", requirements: "任职要求：\n本科及以上学历\n加分项：\n计算机相关专业\n3年工作经验", wantMajorBonus: true},
		{name: "zero_range", requirements: "本科及以上学历，计算机相关专业；0-1年工作经验"},
		{name: "zero_field", requirements: "本科及以上学历，计算机相关专业", field: "0-1年"},
		{name: "one_year", requirements: "本科及以上学历，计算机相关专业；有1年工作经验", wantYear: true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			job := object(beisenFixture(t, "miniso-detail-1f528032-29cd-4384-93de-ef66e6c96a9f")["Data"])
			job["Require"], job["YearsOfWorking"] = sample.requirements, sample.field
			doc, reason, err := beisenDocument(site, job)
			if err != nil || reason != "" {
				t.Fatalf("valid campus role was rejected: reason=%q err=%v", reason, err)
			}
			variant := doc.Variants[0]
			if strings.Contains(variant.Requirements, "至少1年工作经验") != sample.wantYear {
				t.Fatal("project/zero experience was promoted to one year employment")
			}
			if sample.name == "bonus_section" && (strings.Contains(variant.Requirements, "3年") || strings.Contains(variant.Requirements, "硕士") || !strings.Contains(variant.Preferred, "3年工作经验")) {
				t.Fatal("section-level bonus requirements became hard conditions")
			}
			if sample.wantMajorBonus && variant.Major.Kind != "preferred" {
				t.Fatal("major from bonus section became a required major")
			}
			zero := 0
			result := matching.Evaluate(profile.Candidate{Education: "bachelor", ExperienceMonths: &zero}, catalog.Job{Document: doc}, variant)
			if sample.wantYear && result.Status != "ineligible" {
				t.Fatal("explicit one-year work requirement did not reject zero experience")
			}
			if !sample.wantYear && result.Status == "ineligible" {
				t.Fatal("bonus/project/zero-year condition rejected a bachelor with zero employment experience")
			}
		})
	}
}

func TestBeisenRealDocumentHashesRemainStable(t *testing.T) {
	for _, sample := range []struct {
		source, prefix, expected string
		ids                      []string
	}{
		{"miniso-campus", "miniso", "40f7b9c350c5dc1e02900d1d84619499f45e769777f37009878d39cead9aa8e7", []string{"1f528032-29cd-4384-93de-ef66e6c96a9f", "b336d5cd-5a95-45f2-ac23-bc3159c86a42"}},
		{"mxbc-campus", "mxbc", "3a2a671f12f8b0655530447d6157da891b5ef90cc275e3cb574ed4b7691c9596", []string{"428a316e-18c0-4299-a872-c8d68ae4918e", "f71f265b-9ba5-4ca5-ae7e-d88ceb0f45c6"}},
	} {
		t.Run(sample.source, func(t *testing.T) {
			site, _ := beisenConfig(sample.source)
			documents := []catalog.Document{}
			for _, id := range sample.ids {
				job := object(beisenFixture(t, sample.prefix+"-detail-"+id)["Data"])
				doc, reason, err := beisenDocument(site, job)
				if err != nil || reason != "" {
					t.Fatalf("real detail rejected: reason=%q err=%v", reason, err)
				}
				documents = append(documents, doc)
			}
			sort.Slice(documents, func(i, j int) bool { return documents[i].ExternalID < documents[j].ExternalID })
			body, err := json.Marshal(documents)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(body)
			if got := hex.EncodeToString(sum[:]); got != sample.expected {
				t.Fatalf("production content changed: got=%s expected=%s", got, sample.expected)
			}
		})
	}
}
