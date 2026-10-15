package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) any {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
func writeFixture(t *testing.T, w http.ResponseWriter, name string) {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Error(err)
	}
}

func TestTencentCurrentChildBatchAndSuccessfulNullEmpty(t *testing.T) {
	for _, empty := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/v1/position/getProjectMapping":
				writeFixture(t, w, "tencent-mapping.json")
			case "/api/v1/position/searchPosition":
				var data any = map[string]any{"count": 1, "positionList": []any{fixture(t, "tencent-list-entry.json")}}
				if empty {
					data = map[string]any{"count": 0, "positionList": nil}
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"status": 0, "data": data}); err != nil {
					t.Error(err)
				}
			case "/api/v1/jobDetails/getJobDetailsByPostId":
				writeFixture(t, w, "tencent-detail.json")
			default:
				w.WriteHeader(404)
			}
		}))
		reader := NewReader()
		reader.TencentBase = server.URL
		docs, total, err := reader.Tencent(context.Background())
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		if empty {
			if total != 0 || len(docs) != 0 {
				t.Fatal("successful null empty response misclassified")
			}
			continue
		}
		if total != 1 || len(docs) != 1 || docs[0].ExternalID != "1282707383965295616" || docs[0].WrittenTest != "unknown" || docs[0].PublicStatus != "unknown" {
			t.Fatalf("Tencent evidence: %#v", docs)
		}
		if len(docs[0].GraduationRules) != 1 || docs[0].GraduationRules[0].Through != "2027-12-31" || docs[0].GraduationRules[0].From != "2026-01-01" {
			t.Fatal("stale parent menu replaced the current child range")
		}
	}
}

func TestHuaweiIntentionCitiesAndAmbiguousNull(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/cn/campus-recruitment" {
				writeFixture(t, w, "huawei-campus.html")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasSuffix(r.URL.Path, "getJobPage"):
				data := map[string]any{"pageVO": map[string]int{"totalRows": 1}, "result": []any{fixture(t, "huawei-list-entry.json")}}
				if ambiguous {
					data = map[string]any{"pageVO": nil, "result": nil}
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"status": "SUCCESS", "data": data}); err != nil {
					t.Error(err)
				}
			case strings.HasSuffix(r.URL.Path, "getRecruitmentPositionDetail"):
				writeFixture(t, w, "huawei-detail.json")
			case strings.HasSuffix(r.URL.Path, "getPositionIntentionList"):
				writeFixture(t, w, "huawei-intents.json")
			default:
				w.WriteHeader(404)
			}
		}))
		reader := NewReader()
		reader.HuaweiBase = server.URL
		reader.HuaweiAPI = server.URL
		docs, _, err := reader.Huawei(context.Background())
		server.Close()
		if ambiguous {
			if !errors.Is(err, ErrSchema) {
				t.Fatalf("ambiguous null became empty: %v", err)
			}
			continue
		}
		if err != nil || len(docs) != 1 || docs[0].WrittenTest != "yes" {
			t.Fatalf("Huawei evidence: %#v %v", docs, err)
		}
		found := false
		for _, variant := range docs[0].Variants {
			if variant.Name != "AI数据系统" {
				continue
			}
			found = true
			for _, place := range variant.Locations {
				if place.City == "合肥市" {
					t.Fatal("advertisement-level Hefei leaked into the data-system intention")
				}
			}
		}
		if !found {
			t.Fatal("real AI data-system intention disappeared")
		}
	}
}

func TestCMBPreferenceAndNontechnicalControl(t *testing.T) {
	details := array(fixture(t, "cmb-details.json"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "getList") {
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
				return
			}
			items := []any{}
			if text(input["recruitmentTypeId"]) == "DF94FD6D-26D3-4A19-9E69-577C4BA1DE82" {
				for _, detail := range details {
					items = append(items, map[string]any{"publishGID": object(object(detail)["body"])["publishGID"]})
				}
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"returnCode": "SUC0000", "body": map[string]any{"total": len(items), "data": items}}); err != nil {
				t.Error(err)
			}
			return
		}
		for _, detail := range details {
			if text(object(object(detail)["body"])["publishGID"]) == r.URL.Query().Get("publishId") {
				if err := json.NewEncoder(w).Encode(detail); err != nil {
					t.Error(err)
				}
				return
			}
		}
		w.WriteHeader(404)
	}))
	defer server.Close()
	reader := NewReader()
	reader.CMBBase = server.URL
	docs, total, err := reader.CMB(context.Background())
	if err != nil || total != 3 || len(docs) != 2 {
		t.Fatalf("technical filter: %#v %d %v", docs, total, err)
	}
	for _, doc := range docs {
		if doc.GraduationPreference != "2027届优先" || len(doc.GraduationRules) != 0 || doc.WrittenTest != "unknown" || doc.DeadlineAt != nil || doc.DeadlineTimezone != "unknown" || doc.MinimumInternshipMonths == nil || *doc.MinimumInternshipMonths != 1 || doc.DaysPerWeek == nil || *doc.DaysPerWeek != 5 {
			t.Fatalf("internship conditions changed: %#v", doc)
		}
	}
}
