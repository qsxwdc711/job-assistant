package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func tencentInternshipServer(t *testing.T, mode string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		respond := func(data any) {
			if err := json.NewEncoder(w).Encode(map[string]any{"status": 0, "data": data}); err != nil {
				t.Error(err)
			}
		}
		switch request.URL.Path {
		case "/api/v1/position/getProjectMapping":
			mapping := object(fixture(t, "internships/tencent-mapping.json"))
			if mode == "missing_mapping" {
				parent := object(array(mapping["data"])[1])
				parent["subProjectList"] = array(parent["subProjectList"])[:1]
			}
			respond(mapping["data"])
		case "/api/v1/position/searchPosition":
			var input map[string]any
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&input); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			ids := array(input["projectMappingIdList"])
			if len(ids) != 1 || text(input["workCountryType"]) != "1" {
				t.Error("internship request must select one domestic mapping")
				w.WriteHeader(400)
				return
			}
			mapping := text(ids[0])
			if mapping != "2" && mapping != "104" {
				t.Errorf("unexpected internship batch: %s", mapping)
			}
			if mode == "later_failure" && mapping == "104" {
				w.WriteHeader(503)
				return
			}
			items := []any{}
			for _, job := range array(fixture(t, "internships/tencent-jobs.json")) {
				if text(object(job)["mappingId"]) == mapping {
					items = append(items, job)
				}
			}
			total := len(items)
			if mode == "project_pool" && mapping == "104" {
				items = append(items, fixture(t, "internships/tencent-project-entry.json"))
				total = len(items)
			}
			if mode == "empty" {
				total, items = 0, nil
			}
			if mode == "malformed_empty" {
				respond(map[string]any{"count": 0, "positionList": map[string]any{}})
				return
			}
			if mode == "cross_duplicate" && mapping == "104" {
				items = append(items, array(fixture(t, "internships/tencent-jobs.json"))[0])
				total = len(items)
			}
			if (mode == "changing_total" || mode == "page_duplicate") && mapping == "104" {
				total, items = 2, items[:1]
				if mode == "changing_total" && text(input["pageIndex"]) == "2" {
					total = 3
				}
			}
			respond(map[string]any{"count": total, "positionList": items})
		case "/api/v1/jobDetails/getJobDetailsByPostId":
			id := request.URL.Query().Get("postId")
			if id == "-2" {
				writeFixture(t, w, "internships/tencent-project-pool.json")
				return
			}
			detail := object(object(fixture(t, "internships/tencent-"+id+".json"))["data"])
			if mode == "missing_detail" {
				detail["desc"] = ""
			}
			respond(detail)
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestTencentInternshipBatchConditionsAndNontechnicalControl(t *testing.T) {
	server := tencentInternshipServer(t, "success")
	defer server.Close()
	reader := NewReader()
	reader.TencentBase = server.URL
	docs, total, err := reader.TencentInternships(context.Background())
	if err != nil || total != 4 || len(docs) != 3 || len(reader.Exclusions) != 1 {
		t.Fatalf("internship snapshot: total=%d docs=%d exclusions=%d err=%v", total, len(docs), len(reader.Exclusions), err)
	}
	if reader.Exclusions[0].ExternalID != "1210646470286783489" {
		t.Fatal("visual design control was not excluded")
	}
	for _, doc := range docs {
		if doc.SourceCode != "tencent-internships" || doc.RecruitmentType != "internship" || doc.WrittenTest != "unknown" || doc.MinimumInternshipMonths != nil || doc.DaysPerWeek != nil {
			t.Fatalf("internship conditions changed for %s", doc.ExternalID)
		}
		if doc.ExternalID == "1200791473415778304" {
			if doc.CampaignKey != "intern:2:2026" || doc.StudentRequired != "unknown" || len(doc.GraduationRules) != 1 || doc.GraduationRules[0].From != "2026-09-01" || doc.GraduationRules[0].Through != "2027-12-31" {
				t.Fatal("ordinary internship inherited stale parent graduation range")
			}
		} else if doc.CampaignKey != "intern:104:2026" || doc.StudentRequired != "yes" || len(doc.GraduationRules) != 0 || !strings.Contains(doc.CampaignEvidence, "面向全体在校生") {
			t.Fatal("daily internship inherited a sibling graduation gate")
		}
		if doc.Variants[0].Responsibilities == "" || doc.Variants[0].Requirements == "" || len(doc.Variants[0].Departments) == 0 {
			t.Fatal("real internship duties, requirements or department places disappeared")
		}
	}
}

func TestTencentInternshipRejectsIncompleteSnapshot(t *testing.T) {
	for _, mode := range []string{"later_failure", "changing_total", "page_duplicate", "cross_duplicate", "missing_mapping", "missing_detail", "malformed_empty"} {
		t.Run(mode, func(t *testing.T) {
			server := tencentInternshipServer(t, mode)
			defer server.Close()
			reader := NewReader()
			reader.TencentBase = server.URL
			docs, _, err := reader.TencentInternships(context.Background())
			if err == nil || len(docs) != 0 {
				t.Fatalf("partial internship batch accepted: docs=%d err=%v", len(docs), err)
			}
			if mode != "later_failure" && !errors.Is(err, ErrSchema) {
				t.Fatalf("invalid snapshot error: %v", err)
			}
		})
	}
}

func TestTencentInternshipSuccessfulEmpty(t *testing.T) {
	server := tencentInternshipServer(t, "empty")
	defer server.Close()
	reader := NewReader()
	reader.TencentBase = server.URL
	docs, total, err := reader.TencentInternships(context.Background())
	if err != nil || total != 0 || len(docs) != 0 {
		t.Fatalf("explicit empty snapshot: docs=%d total=%d err=%v", len(docs), total, err)
	}
}

func TestTencentInternshipProjectPoolKeepsDirectionIdentity(t *testing.T) {
	server := tencentInternshipServer(t, "project_pool")
	defer server.Close()
	reader := NewReader()
	reader.TencentBase = server.URL
	docs, total, err := reader.TencentInternships(context.Background())
	if err != nil || total != 5 || len(docs) != 4 {
		t.Fatalf("project pool snapshot: docs=%d total=%d err=%v", len(docs), total, err)
	}
	for _, doc := range docs {
		if doc.ExternalID != "-2" {
			continue
		}
		if len(doc.Variants) != 2 || doc.Variants[0].Key != "1216426561818062848" || doc.Variants[1].Key != "1226320039750543360" || doc.Variants[0].Direction != "backend" || doc.Variants[1].Direction != "security" {
			t.Fatal("project direction identities were dropped or merged")
		}
		for _, variant := range doc.Variants {
			if !strings.HasPrefix(variant.Requirements, "项目共同要求：") || len(variant.Departments) == 0 || len(variant.Locations) != 1 || variant.Locations[0].City != "深圳市" {
				t.Fatal("project shared conditions or direction places disappeared")
			}
		}
		return
	}
	t.Fatal("verified project internship was silently excluded")
}

func TestTencentInternshipProjectPoolRejectsAmbiguousIdentity(t *testing.T) {
	for _, mode := range []string{"wrong_parent", "missing_children", "duplicate_child", "partial_child_conditions"} {
		t.Run(mode, func(t *testing.T) {
			detail := object(object(fixture(t, "internships/tencent-project-pool.json"))["data"])
			groups := array(detail["projectInternDirections"])
			children := array(object(groups[0])["subProjectInterns"])
			switch mode {
			case "wrong_parent":
				detail["projectId"] = "20"
			case "missing_children":
				object(groups[0])["subProjectInterns"] = nil
			case "duplicate_child":
				object(groups[0])["subProjectInterns"] = append(children, children[0])
			case "partial_child_conditions":
				object(children[0])["desc"] = "独立职责已经变更，但要求不完整"
			}
			if _, err := tencentProjectVariants(detail); !errors.Is(err, ErrSchema) {
				t.Fatalf("ambiguous project %s accepted: %v", mode, err)
			}
		})
	}
	detail := object(object(fixture(t, "internships/tencent-project-pool.json"))["data"])
	if _, err := tencentVariants(tencentProgramme{recruitmentType: "campus"}, 1, "-2", detail); !errors.Is(err, ErrSchema) {
		t.Fatal("project pool exception leaked into full-time recruitment")
	}
}

func TestTencentInternshipProjectChildConditionsAndPlacesWin(t *testing.T) {
	detail := object(object(fixture(t, "internships/tencent-project-pool.json"))["data"])
	child := object(array(object(array(detail["projectInternDirections"])[0])["subProjectInterns"])[0])
	child["desc"], child["request"] = "软件系统开发", "计算机专业，硕士及以上"
	child["workCityList"] = []any{"上海"}
	variants, err := tencentProjectVariants(detail)
	if err != nil || len(variants) != 2 || variants[0].Requirements != "计算机专业，硕士及以上" || variants[0].Locations[0].City != "上海市" {
		t.Fatalf("child-specific conditions overwritten by parent: %v", err)
	}
}

func huaweiInternshipServer(t *testing.T, mode string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/cn/campus-recruitment" {
			name := "internships/huawei-campus.html"
			if mode == "missing_internship" {
				name = "huawei-campus.html"
			}
			writeFixture(t, w, name)
			return
		}
		if !strings.HasSuffix(request.Header.Get("Referer"), "recruitmentType=INTERN") {
			t.Error("internship request used full-time Referer")
		}
		var input map[string]any
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(request.URL.Path, "getJobPage"):
			types := array(input["recruitmentType"])
			if len(types) != 1 || text(types[0]) != "INTERN" || text(input["jobType"]) != "CR" {
				t.Error("internship list used full-time filter")
			}
			items := array(fixture(t, "internships/huawei-jobs.json"))
			var pageVO any = map[string]int{"totalRows": len(items)}
			if mode == "ambiguous_empty" {
				pageVO, items = nil, nil
			}
			if mode == "empty" {
				pageVO, items = map[string]int{"totalRows": 0}, nil
			}
			if mode == "malformed_empty" {
				if err := json.NewEncoder(w).Encode(map[string]any{"status": "SUCCESS", "data": map[string]any{"pageVO": map[string]int{"totalRows": 0}, "result": map[string]any{}}}); err != nil {
					t.Error(err)
				}
				return
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"status": "SUCCESS", "data": map[string]any{"pageVO": pageVO, "result": items}}); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(request.URL.Path, "getRecruitmentPositionDetail"):
			writeFixture(t, w, "internships/huawei-"+text(input["advertisementId"])+".json")
		case strings.HasSuffix(request.URL.Path, "getPositionIntentionList"):
			writeFixture(t, w, "internships/huawei-intents-"+text(input["jobId"])+".json")
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestHuaweiInternshipPreferenceAndIntentionPlaces(t *testing.T) {
	server := huaweiInternshipServer(t, "success")
	defer server.Close()
	reader := NewReader()
	reader.HuaweiBase, reader.HuaweiAPI = server.URL, server.URL
	docs, total, err := reader.HuaweiInternships(context.Background())
	if err != nil || total != 3 || len(docs) != 3 {
		t.Fatalf("internship snapshot: docs=%d total=%d err=%v", len(docs), total, err)
	}
	for _, doc := range docs {
		if doc.SourceCode != "huawei-internships" || doc.RecruitmentType != "internship" || doc.StudentRequired != "yes" || doc.CampaignKey != "intern:2027" || len(doc.GraduationRules) != 0 || !strings.Contains(doc.GraduationPreference, "主要面向2027届") || doc.WrittenTest != "unknown" || doc.WrittenTestEvidence != "" || doc.MinimumInternshipMonths != nil || doc.DaysPerWeek != nil {
			t.Fatalf("full-time conditions leaked into internship %s", doc.ExternalID)
		}
		if strings.Contains(doc.CampaignEvidence, "2027年1月1日") || !strings.Contains(doc.CampaignEvidence, "在校") {
			t.Fatal("internship campaign evidence mixed with full-time dates")
		}
		var jobID string
		for _, entry := range array(fixture(t, "internships/huawei-jobs.json")) {
			if text(object(entry)["advertisementId"]) == doc.ExternalID {
				jobID = text(object(entry)["jobId"])
			}
		}
		intents := array(object(fixture(t, "internships/huawei-intents-"+jobID+".json"))["data"])
		for _, variant := range doc.Variants {
			found := false
			for _, entry := range intents {
				intent := object(entry)
				if text(intent["positionIntentionId"]) != variant.Key {
					continue
				}
				found = true
				if !reflect.DeepEqual(variant.Locations, Locations(text(intent["jobPlaceName"]))) || len(variant.Departments) != len(array(intent["deptAndPlaceList"])) {
					t.Fatal("intention and its applicable places were mixed")
				}
				for i, group := range array(intent["deptAndPlaceList"]) {
					if !reflect.DeepEqual(variant.Departments[i].Locations, Locations(text(object(group)["jobPlaceName"]))) || variant.Departments[i].Code != text(object(group)["deptCode"]) {
						t.Fatal("department/place relationship changed")
					}
				}
			}
			if !found {
				t.Fatalf("invented intention %s", variant.Key)
			}
		}
	}
}

func TestHuaweiInternshipRejectsMissingCampaignAndAmbiguousEmpty(t *testing.T) {
	for _, mode := range []string{"missing_internship", "ambiguous_empty", "malformed_empty"} {
		t.Run(mode, func(t *testing.T) {
			server := huaweiInternshipServer(t, mode)
			defer server.Close()
			reader := NewReader()
			reader.HuaweiBase, reader.HuaweiAPI = server.URL, server.URL
			docs, _, err := reader.HuaweiInternships(context.Background())
			if !errors.Is(err, ErrSchema) || len(docs) != 0 {
				t.Fatalf("unknown internship schema accepted: docs=%d err=%v", len(docs), err)
			}
		})
	}
}

func TestHuaweiInternshipSuccessfulEmpty(t *testing.T) {
	server := huaweiInternshipServer(t, "empty")
	defer server.Close()
	reader := NewReader()
	reader.HuaweiBase, reader.HuaweiAPI = server.URL, server.URL
	docs, total, err := reader.HuaweiInternships(context.Background())
	if err != nil || total != 0 || len(docs) != 0 {
		t.Fatalf("explicit empty snapshot: docs=%d total=%d err=%v", len(docs), total, err)
	}
}

func TestHuaweiInternshipCampaignRejectsChangedAudience(t *testing.T) {
	body, err := os.ReadFile("testdata/internships/huawei-campus.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []string{"仅面向2027届", "主要面向2028届"} {
		changed := strings.ReplaceAll(string(body), "主要面向2027届", replacement)
		if _, err := huaweiInternshipCampaign([]byte(changed)); !errors.Is(err, ErrSchema) {
			t.Fatalf("changed audience %q silently retained old preference: %v", replacement, err)
		}
	}
}
