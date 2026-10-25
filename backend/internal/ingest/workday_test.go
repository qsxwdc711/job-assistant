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
	"sync"
	"sync/atomic"
	"testing"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/profile"
)

func workdayFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	file, err := os.Open("testdata/workday/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestWorkdayRealQualificationsAndLocations(t *testing.T) {
	site, ok := workdayConfig("nvidia-careers")
	if !ok {
		t.Fatal("verified NVIDIA source missing")
	}
	for _, tc := range []struct {
		id, kind, education string
		student             bool
		cities              int
	}{
		{"JR2026164", "internship", "master", true, 2},
		{"JR2026546", "internship", "bachelor", true, 2},
		{"JR2026972", "internship", "master", true, 1},
		{"JR2023566", "campus", "bachelor", false, 3},
	} {
		t.Run(tc.id, func(t *testing.T) {
			detail := workdayFixture(t, "nvidia-"+tc.id)
			doc, reason, err := workdayDocument(site, workdayType{kind: tc.kind, name: tc.kind}, detail)
			if err != nil || reason != "" {
				t.Fatalf("real sample rejected: %s %v", reason, err)
			}
			if doc.ExternalID != tc.id || doc.CompanyCode != "nvidia" || doc.RecruitmentType != tc.kind || len(doc.Variants) != 1 || len(doc.Variants[0].Locations) != tc.cities || !doc.LoginRequired || doc.ApplyLevel != "job" || doc.DeadlineAt != nil || doc.DeadlineDate != "" || len(doc.GraduationRules) != 0 || doc.WrittenTest != "unknown" {
				t.Fatalf("identity, types or unknown facts changed: %#v", doc)
			}
			for _, loc := range doc.Variants[0].Locations {
				if loc.Kind != "domestic" || loc.Country != "中国" || loc.Province == "unknown" {
					t.Fatalf("China location not normalized: %#v", loc)
				}
			}
			if (doc.StudentRequired == "yes") != tc.student {
				t.Fatalf("student evidence lost: %s", doc.StudentRequired)
			}
			student := true
			candidate := profile.Candidate{Education: tc.education, MajorCodes: []string{"computer_science"}, Student: &student}
			for _, check := range matching.Evaluate(candidate, catalog.Job{Document: doc}, doc.Variants[0]).Checks {
				if (check.Field == "education" || check.Field == "major" || (check.Field == "student" && tc.student)) && check.Status != "eligible" {
					t.Fatalf("confirmed real qualification not eligible: %#v", check)
				}
			}
			if tc.education == "master" {
				candidate.Education = "bachelor"
				if matching.Evaluate(candidate, catalog.Job{Document: doc}, doc.Variants[0]).Status != "ineligible" {
					t.Fatal("English masters minimum did not reject a bachelor")
				}
			}
			if tc.id == "JR2023566" && !strings.Contains(doc.CampaignEvidence, "MS or PhD preferred") {
				t.Fatal("mixed hard bachelor and preferred higher degrees lost")
			}
		})
	}
}

func TestWorkdayRejectsUnscopedAndConflictingRoles(t *testing.T) {
	site, _ := workdayConfig("nvidia-careers")
	for _, tc := range []struct {
		name, kind, title, requirements string
	}{
		{"unscoped social", "early_career", "Software Engineer", "Bachelor in Computer Science. Experience with C++."},
		{"hard experience", "early_career", "Software Engineer", "Bachelor in Computer Science. At least 3 years of professional work experience."},
		{"preferred experience only", "early_career", "Software Engineer", "Bachelor in Computer Science. One year of professional work experience preferred."},
		{"nontechnical CS major", "internship", "International Marketing Intern", "Bachelor in Computer Science. Knowledge of CUDA."},
		{"management title", "campus", "Software Engineering Manager", "Bachelor in Computer Science. Knowledge of C++."},
		{"physical hardware", "campus", "Physical Design, VLSI - New College Grad", "Master in Engineering. Python preferred."},
		{"native type conflict", "campus", "Software Engineering Intern", "Pursuing a Master in Computer Science."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail := workdayFixture(t, "nvidia-JR2026972")
			info := object(detail["jobPostingInfo"])
			info["title"] = tc.title
			info["jobDescription"] = "<p>Develop software with C++.</p><p>What we need to see:</p><p>" + tc.requirements + "</p>"
			doc, reason, err := workdayDocument(site, workdayType{kind: tc.kind, name: tc.kind}, detail)
			if err != nil || reason == "" || doc.ExternalID != "" {
				t.Fatalf("out-of-scope role published: %#v %s %v", doc, reason, err)
			}
		})
	}
}

func TestWorkdayOneYearHardWorkAndPreferredRemainSeparate(t *testing.T) {
	site, _ := workdayConfig("nvidia-careers")
	detail := workdayFixture(t, "nvidia-JR2026972")
	info := object(detail["jobPostingInfo"])
	info["title"] = "Software Engineer"
	info["jobDescription"] = "<p>Develop software.</p><p>What we need to see:</p><p>Bachelor in Computer Science or related field.</p><p>At least 1 year of professional work experience.</p><p>Ways to stand out from the crowd:</p><p>3 years of CUDA experience.</p>"
	doc, reason, err := workdayDocument(site, workdayType{kind: "early_career", name: "Regular Employee"}, detail)
	if err != nil || reason != "" || !strings.Contains(doc.Variants[0].Requirements, "至少1年工作经验") || !strings.Contains(doc.Variants[0].Preferred, "3 years") {
		t.Fatalf("hard/preferred boundaries incorrect: %#v %s %v", doc, reason, err)
	}
	zero, student := 0, false
	candidate := profile.Candidate{Education: "bachelor", MajorCodes: []string{"computer_science"}, ExperienceMonths: &zero, Student: &student}
	if matching.Evaluate(candidate, catalog.Job{Document: doc}, doc.Variants[0]).Status != "ineligible" {
		t.Fatal("zero-experience candidate passed explicit one-year work requirement")
	}
	info["jobDescription"] = "<p>What we need to see:</p><p>Bachelor in Computer Science; MS or PhD preferred.</p><p>New graduates are welcome.</p>"
	doc, reason, err = workdayDocument(site, workdayType{kind: "early_career", name: "Regular Employee"}, detail)
	if err != nil || reason != "" || doc.MinimumEducation != "bachelor_or_above" {
		t.Fatalf("preferred higher degree replaced hard bachelor: %#v %s %v", doc, reason, err)
	}
}

func workdayTestPage(t *testing.T, kind workdayType, jobs []any, total int) map[string]any {
	t.Helper()
	page := workdayFixture(t, "nvidia-intern-list")
	if jobs == nil {
		jobs = []any{}
	}
	page["jobPostings"], page["total"] = jobs, total
	var fix func([]any)
	fix = func(values []any) {
		for _, raw := range values {
			v := object(raw)
			if text(v["id"]) == kind.id || text(v["id"]) == "2fcb99c455831013ea529fe151e3323c" {
				v["count"] = total
			}
			fix(array(v["values"]))
		}
	}
	fix(array(page["facets"]))
	return page
}

func workdayTestServer(t *testing.T, alter func(string, map[string]any) (int, any)) (*Reader, *atomic.Int32) {
	t.Helper()
	site, _ := workdayConfig("nvidia-careers")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		if req.URL.Path == "/wday/cxs/nvidia/NVIDIAExternalCareerSite/jobs" {
			var input map[string]any
			decoder := json.NewDecoder(req.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&input); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if req.Method != http.MethodPost || text(input["searchText"]) != "" || text(input["limit"]) != "20" {
				t.Error("list must use native filters and observed page limit")
			}
			facets := object(input["appliedFacets"])
			if joinedStrings(facets["locationHierarchy1"], "") != site.china {
				t.Error("China native filter missing")
			}
			typeID := joinedStrings(facets["workerSubType"], "")
			var kind workdayType
			for _, option := range site.types {
				if option.id == typeID {
					kind = option
				}
			}
			if kind.id == "" {
				t.Error("unverified native recruitment type")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var jobs []any
			if kind.kind == "internship" {
				jobs = array(workdayFixture(t, "nvidia-intern-list")["jobPostings"])[:2]
			}
			if kind.kind == "campus" {
				jobs = array(workdayFixture(t, "nvidia-graduate-list")["jobPostings"])[1:2]
			}
			page := workdayTestPage(t, kind, jobs, len(jobs))
			if alter != nil {
				if status, body := alter(kind.kind, page); status != 0 {
					w.WriteHeader(status)
					if err := json.NewEncoder(w).Encode(body); err != nil {
						t.Error(err)
					}
					return
				}
			}
			if err := json.NewEncoder(w).Encode(page); err != nil {
				t.Error(err)
			}
			return
		}
		if req.Method != http.MethodGet || !strings.HasPrefix(req.URL.Path, "/wday/cxs/nvidia/NVIDIAExternalCareerSite/job/China-Shanghai/") {
			t.Errorf("unexpected public path %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		for _, id := range []string{"JR2026546", "JR2026972", "JR2023566"} {
			if strings.HasSuffix(req.URL.Path, "_"+id) {
				detail := workdayFixture(t, "nvidia-"+id)
				if alter != nil {
					if status, body := alter(id, detail); status != 0 {
						w.WriteHeader(status)
						if err := json.NewEncoder(w).Encode(body); err != nil {
							t.Error(err)
						}
						return
					}
				}
				if err := json.NewEncoder(w).Encode(detail); err != nil {
					t.Error(err)
				}
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	r := NewReader()
	r.WorkdayBase = server.URL
	return r, &requests
}

func TestWorkdayNativeScopeFullResultAndStableContent(t *testing.T) {
	r, requests := workdayTestServer(t, nil)
	docs, fetched, err := r.Workday(context.Background(), "nvidia-careers")
	if err != nil || fetched != 3 || len(docs) != 3 || requests.Load() != 6 {
		t.Fatalf("complete source: docs=%d fetched=%d requests=%d error=%v", len(docs), fetched, requests.Load(), err)
	}
	r2, _ := workdayTestServer(t, func(key string, body map[string]any) (int, any) {
		if strings.HasPrefix(key, "JR") {
			info := object(body["jobPostingInfo"])
			info["postedOn"], info["startDate"] = "Posted Yesterday", "2026-10-22"
			return http.StatusOK, body
		}
		return 0, nil
	})
	docs2, _, err := r2.Workday(context.Background(), "nvidia-careers")
	if err != nil || !reflect.DeepEqual(docs, docs2) {
		t.Fatal("transport timestamps changed stable job content", err)
	}
}

func TestWorkdayRejectsIncompleteOrRestrictedResultWithoutPartialDocuments(t *testing.T) {
	for _, name := range []string{"restricted late detail", "closed detail", "wrong identity", "untrusted apply URL", "duplicate ID", "missing facets", "short page", "native filter changed", "null"} {
		t.Run(name, func(t *testing.T) {
			r, _ := workdayTestServer(t, func(key string, body map[string]any) (int, any) {
				if key == "campus" {
					switch name {
					case "duplicate ID":
						body["jobPostings"] = []any{array(workdayFixture(t, "nvidia-intern-list")["jobPostings"])[0]}
					case "missing facets":
						delete(body, "facets")
					case "short page":
						body["total"] = 2
					case "native filter changed":
						body["facets"] = []any{}
					case "null":
						return http.StatusOK, nil
					}
					return http.StatusOK, body
				}
				if key == "JR2023566" {
					info := object(body["jobPostingInfo"])
					switch name {
					case "restricted late detail":
						return http.StatusForbidden, nil
					case "closed detail":
						info["canApply"] = false
					case "wrong identity":
						info["jobReqId"] = "JR0000000"
					case "untrusted apply URL":
						info["externalUrl"] = "https://example.com/apply"
					}
					return http.StatusOK, body
				}
				return 0, nil
			})
			docs, _, err := r.Workday(context.Background(), "nvidia-careers")
			if err == nil || len(docs) != 0 {
				t.Fatalf("incomplete result returned documents=%d error=%v", len(docs), err)
			}
			if name == "restricted late detail" && !errors.Is(err, ErrRestricted) {
				t.Fatalf("challenge not reported as restricted: %v", err)
			}
		})
	}
}

func TestWorkdayCompletePaginationAndLateListFailure(t *testing.T) {
	for _, mode := range []string{"complete", "zero metadata sentinel", "changed total", "changed anchor total", "changed anchor identity", "duplicate next page", "missing next page", "empty"} {
		t.Run(mode, func(t *testing.T) {
			site, _ := workdayConfig("nvidia-careers")
			var offsets []int
			var offsetsMu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if strings.HasSuffix(req.URL.Path, "/jobs") {
					var input map[string]any
					decoder := json.NewDecoder(req.Body)
					decoder.UseNumber()
					if err := decoder.Decode(&input); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					offset, err := count(input["offset"])
					if err != nil {
						t.Error(err)
					}
					typeID := joinedStrings(object(input["appliedFacets"])["workerSubType"], "")
					kind := site.types[0]
					for _, candidate := range site.types {
						if candidate.id == typeID {
							kind = candidate
						}
					}
					total := 0
					var jobs []any
					if kind.kind == "internship" && mode != "empty" {
						offsetsMu.Lock()
						offsets = append(offsets, offset)
						anchorRequest := offset == 0 && len(offsets) > 1
						offsetsMu.Unlock()
						total = 21
						for i := offset; i < min(offset+20, total); i++ {
							id := fmt.Sprintf("JR9999%03d", i)
							if offset == 20 && mode == "duplicate next page" {
								id = "JR9999000"
							}
							jobs = append(jobs, map[string]any{"title": "Software Engineering Intern - 2027", "externalPath": "/job/China-Shanghai/Software-Engineering-Intern---2027_" + id, "bulletFields": []string{id}})
						}
						if offset == 20 && mode == "changed total" {
							total++
							jobs = append(jobs, map[string]any{"title": "Software Engineering Intern - 2027", "externalPath": "/job/China-Shanghai/Software-Engineering-Intern---2027_JR9999021", "bulletFields": []string{"JR9999021"}})
						}
						if offset == 20 && mode == "missing next page" {
							jobs = nil
						}
						if anchorRequest && mode == "changed anchor total" {
							total++
						}
						if anchorRequest && mode == "changed anchor identity" {
							job := object(jobs[0])
							job["bulletFields"] = []string{"JR9999099"}
							job["externalPath"] = "/job/China-Shanghai/Software-Engineering-Intern---2027_JR9999099"
						}
					}
					page := workdayTestPage(t, kind, jobs, total)
					if offset == 20 && mode == "zero metadata sentinel" {
						observed := workdayFixture(t, "nvidia-intern-offset20")
						page["total"], page["facets"] = observed["total"], observed["facets"]
					}
					if err := json.NewEncoder(w).Encode(page); err != nil {
						t.Error(err)
					}
					return
				}
				if mode != "complete" && mode != "zero metadata sentinel" {
					t.Error("incomplete list must not request any job detail")
					w.WriteHeader(http.StatusNotFound)
					return
				}
				detail := workdayFixture(t, "nvidia-JR2026972")
				info := object(detail["jobPostingInfo"])
				postingID := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
				id := postingID[strings.LastIndex(postingID, "_")+1:]
				info["jobReqId"], info["jobPostingId"] = id, postingID
				info["externalUrl"] = site.host + "/" + site.site + "/job/China-Shanghai/" + postingID
				if err := json.NewEncoder(w).Encode(detail); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			r := NewReader()
			r.WorkdayBase = server.URL
			docs, fetched, err := r.Workday(context.Background(), "nvidia-careers")
			offsetsMu.Lock()
			receivedOffsets := append([]int(nil), offsets...)
			offsetsMu.Unlock()
			switch mode {
			case "complete", "zero metadata sentinel":
				if err != nil || len(docs) != 21 || fetched != 21 || !reflect.DeepEqual(receivedOffsets, []int{0, 20, 0}) {
					t.Fatalf("pagination incomplete: docs=%d fetched=%d offsets=%v err=%v", len(docs), fetched, receivedOffsets, err)
				}
			case "empty":
				if err != nil || len(docs) != 0 || fetched != 0 {
					t.Fatalf("explicit complete empty response: docs=%d fetched=%d err=%v", len(docs), fetched, err)
				}
			default:
				if err == nil || len(docs) != 0 {
					t.Fatalf("late list failure returned partial result: docs=%d fetched=%d err=%v", len(docs), fetched, err)
				}
			}
		})
	}
}

func TestWorkdayInternshipHardCommitmentsAndCancelledContext(t *testing.T) {
	site, _ := workdayConfig("nvidia-careers")
	detail := workdayFixture(t, "nvidia-JR2026972")
	info := object(detail["jobPostingInfo"])
	info["jobDescription"] = text(info["jobDescription"]) + "<p>Requirements:</p><p>Internship availability: at least 6 months, at least 4 days per week.</p>"
	doc, reason, err := workdayDocument(site, site.types[0], detail)
	if err != nil || reason != "" || doc.MinimumInternshipMonths == nil || *doc.MinimumInternshipMonths != 6 || doc.DaysPerWeek == nil || *doc.DaysPerWeek != 4 {
		t.Fatalf("hard internship commitments lost: %#v %s %v", doc, reason, err)
	}
	r, requests := workdayTestServer(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if docs, _, err := r.Workday(ctx, "nvidia-careers"); !errors.Is(err, context.Canceled) || len(docs) != 0 || requests.Load() != 0 {
		t.Fatalf("cancelled context leaked work: docs=%d requests=%d err=%v", len(docs), requests.Load(), err)
	}
}

func TestWorkdayPreferredStudentAndZeroToOneExperience(t *testing.T) {
	site, _ := workdayConfig("nvidia-careers")
	t.Run("student and duration are preferred", func(t *testing.T) {
		detail := workdayFixture(t, "nvidia-JR2026972")
		info := object(detail["jobPostingInfo"])
		info["jobDescription"] = "<p>Develop software with C++.</p><p>What we need to see:</p><p>Bachelor in Computer Science.</p><p>Currently enrolled in a Master's degree preferred.</p><p>Internship availability: at least 6 months preferred.</p>"
		doc, reason, err := workdayDocument(site, site.types[0], detail)
		if err != nil || reason != "" || doc.StudentRequired != "unknown" || doc.MinimumInternshipMonths != nil || doc.MinimumEducation != "bachelor_or_above" {
			t.Fatalf("preferred condition became a hard constraint: student=%s months=%v education=%s reason=%s err=%v", doc.StudentRequired, doc.MinimumInternshipMonths, doc.MinimumEducation, reason, err)
		}
		if !strings.Contains(doc.CampaignEvidence, "Master's degree preferred") {
			t.Fatal("original preferred evidence discarded")
		}
	})
	for _, wording := range []string{"0 to 1 years of professional work experience.", "0-1 years of professional work experience.", "Zero years of professional work experience."} {
		t.Run(wording, func(t *testing.T) {
			minimum, explicit, workMinimum := workdayExperience(wording)
			if minimum != 0 || !explicit || workMinimum != 0 {
				t.Fatalf("zero minimum changed: minimum=%d explicit=%v workMinimum=%d", minimum, explicit, workMinimum)
			}
			detail := workdayFixture(t, "nvidia-JR2026972")
			info := object(detail["jobPostingInfo"])
			info["title"] = "Software Engineer"
			info["jobDescription"] = "<p>Develop software with C++.</p><p>What we need to see:</p><p>Bachelor in Computer Science.</p><p>" + wording + "</p>"
			doc, reason, err := workdayDocument(site, site.types[2], detail)
			if err != nil || reason != "" || doc.ExternalID == "" || strings.Contains(doc.Variants[0].Requirements, "至少1年工作经验") {
				t.Fatalf("zero-experience opportunity altered: id=%s reason=%s err=%v", doc.ExternalID, reason, err)
			}
		})
	}
	t.Run("academic clause cannot erase hard professional experience", func(t *testing.T) {
		minimum, explicit, workMinimum := workdayExperience("At least 3 years of professional work experience; academic project experience is also valued.")
		if minimum != 3 || !explicit || workMinimum != 3 {
			t.Fatalf("academic text erased a work minimum: minimum=%d explicit=%v workMinimum=%d", minimum, explicit, workMinimum)
		}
	})
}

func TestWorkdayNativeInternTypeDoesNotDependOnTitleKeyword(t *testing.T) {
	site, _ := workdayConfig("nvidia-careers")
	detail := workdayFixture(t, "nvidia-JR2026972")
	info := object(detail["jobPostingInfo"])
	info["title"] = "Software Engineering - 2027"
	doc, reason, err := workdayDocument(site, site.types[0], detail)
	if err != nil || reason != "" || doc.RecruitmentType != "internship" {
		t.Fatalf("authoritative native intern type required a title keyword: kind=%s reason=%s err=%v", doc.RecruitmentType, reason, err)
	}
}
