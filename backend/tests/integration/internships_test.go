package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/ingest"
	"jobassistant/backend/migrations"
)

func internshipFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../internal/ingest/testdata/internships/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func internshipServer(t *testing.T, source, mode string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		write := func(data []byte) {
			if _, err := w.Write(data); err != nil {
				t.Error(err)
			}
		}
		if request.URL.Path == "/cn/campus-recruitment" {
			write(internshipFixture(t, "huawei-campus.html"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(request.URL.Path, "getProjectMapping") {
			write(internshipFixture(t, "tencent-mapping.json"))
			return
		}
		if strings.HasSuffix(request.URL.Path, "getJobDetailsByPostId") {
			name := "tencent-" + request.URL.Query().Get("postId") + ".json"
			if request.URL.Query().Get("postId") == "-2" {
				name = "tencent-project-pool.json"
			}
			write(internshipFixture(t, name))
			return
		}
		var input struct {
			Mappings        []int `json:"projectMappingIdList"`
			AdvertisementID int   `json:"advertisementId"`
			JobID           int   `json:"jobId"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var envelope any
		switch {
		case strings.HasSuffix(request.URL.Path, "searchPosition"):
			if len(input.Mappings) != 1 {
				t.Error("one internship mapping required")
				w.WriteHeader(400)
				return
			}
			id := "1200791473415778304"
			if input.Mappings[0] == 104 {
				if mode == "failure" {
					w.WriteHeader(503)
					return
				}
				id = "1210646470286783488"
			}
			total, items := 1, []any{map[string]any{"postId": id, "positionFamily": 2}}
			if input.Mappings[0] == 104 {
				items = append(items, map[string]any{"postId": "-2", "positionFamily": 2})
				total = len(items)
			}
			if mode == "empty" {
				total, items = 0, nil
			}
			envelope = map[string]any{"status": 0, "data": map[string]any{"count": total, "positionList": items}}
		case strings.HasSuffix(request.URL.Path, "getJobPage"):
			total, items := 1, []any{map[string]any{"advertisementId": 30835, "categoryName": "研发类"}}
			if mode == "empty" {
				total, items = 0, nil
			}
			envelope = map[string]any{"status": "SUCCESS", "data": map[string]any{"pageVO": map[string]int{"totalRows": total}, "result": items}}
		case strings.HasSuffix(request.URL.Path, "getRecruitmentPositionDetail"):
			if mode == "failure" {
				w.WriteHeader(503)
				return
			}
			if input.AdvertisementID != 30835 {
				t.Error("incorrect advertisement identity")
			}
			write(internshipFixture(t, "huawei-30835.json"))
			return
		case strings.HasSuffix(request.URL.Path, "getPositionIntentionList"):
			if input.JobID != 95302 {
				t.Error("incorrect intention job identity")
			}
			write(internshipFixture(t, "huawei-intents-95302.json"))
			return
		default:
			t.Errorf("unexpected path for %s: %s", source, request.URL.Path)
			w.WriteHeader(404)
			return
		}
		if err := json.NewEncoder(w).Encode(envelope); err != nil {
			t.Error(err)
		}
	}))
}

func TestInternshipCollectionPublicationAndSourceIsolation(t *testing.T) {
	for _, source := range []string{"tencent-internships", "huawei-internships"} {
		t.Run(source, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			store := publishEvidence(t, f, evidenceJob())
			if err := migrations.Apply(ctx, f.pool); err != nil {
				t.Fatal(err)
			}
			var sources int
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM sources WHERE code IN ('tencent-internships','huawei-internships') AND enabled").Scan(&sources); err != nil || sources != 2 {
				t.Fatalf("internship source migration: count=%d err=%v", sources, err)
			}
			campusFilter := catalog.Filter{Limit: 20, RecruitmentTypes: []string{"campus"}, IncludeUnknown: true}
			campusBefore, err := store.Jobs(ctx, f.admin.ID, campusFilter, time.Now().UTC())
			if err != nil || campusBefore.Total != 1 {
				t.Fatalf("campus baseline: total=%d err=%v", campusBefore.Total, err)
			}
			collect := func(mode string) ingest.Result {
				t.Helper()
				server := internshipServer(t, source, mode)
				defer server.Close()
				reader := ingest.NewReader()
				reader.TencentBase, reader.HuaweiBase, reader.HuaweiAPI = server.URL, server.URL, server.URL
				return ingest.Collect(ctx, store, reader, source)
			}
			result := collect("success")
			want := 1
			if source == "tencent-internships" {
				want = 3
			}
			if result.Status != "succeeded" || result.Published != want || result.Requests == 0 {
				t.Fatalf("source dispatch/publication: %#v", result)
			}
			filter := catalog.Filter{Limit: 20, RecruitmentTypes: []string{"internship"}, IncludeUnknown: true}
			page, err := store.Jobs(ctx, f.admin.ID, filter, time.Now().UTC())
			if err != nil || page.Total != want {
				t.Fatalf("internship filter: total=%d want=%d err=%v", page.Total, want, err)
			}
			if collect("success").Status != "succeeded" {
				t.Fatal("repeat collection failed")
			}
			for _, job := range page.Items {
				current, err := store.Job(ctx, f.admin.ID, job.ID, time.Now().UTC())
				if err != nil || current.Version != 1 || current.SourceCode != source {
					t.Fatalf("repeat content or internship identity changed: %s err=%v", job.ID, err)
				}
				if source == "huawei-internships" && (len(current.GraduationRules) != 0 || !strings.Contains(current.GraduationPreference, "主要面向2027届")) {
					t.Fatal("preference became a hard graduation gate after persistence")
				}
				if current.ExternalID == "-2" && (len(current.Variants) != 2 || current.Variants[0].Key != "1216426561818062848" || current.Variants[1].Key != "1226320039750543360") {
					t.Fatal("project child identities changed after persistence")
				}
			}
			var contentBefore []byte
			if err := f.pool.QueryRow(ctx, "SELECT jsonb_agg(data ORDER BY id)::text FROM jobs WHERE source_code=$1", source).Scan(&contentBefore); err != nil {
				t.Fatal(err)
			}
			if failed := collect("failure"); failed.Status != "failed" || failed.Published != 0 {
				t.Fatalf("incomplete snapshot published: %#v", failed)
			}
			var contentAfter []byte
			if err := f.pool.QueryRow(ctx, "SELECT jsonb_agg(data ORDER BY id)::text FROM jobs WHERE source_code=$1", source).Scan(&contentAfter); err != nil || !bytes.Equal(contentBefore, contentAfter) {
				t.Fatalf("failure changed prior content: %v", err)
			}
			if empty := collect("empty"); empty.Status != "succeeded" || empty.Published != 0 {
				t.Fatalf("explicit empty snapshot: %#v", empty)
			}
			page, err = store.Jobs(ctx, f.admin.ID, filter, time.Now().UTC())
			if err != nil || page.Total != want {
				t.Fatalf("empty snapshot deleted old internships: total=%d err=%v", page.Total, err)
			}
			campusAfter, err := store.Jobs(ctx, f.admin.ID, campusFilter, time.Now().UTC())
			if err != nil || !reflect.DeepEqual(campusBefore.Items, campusAfter.Items) {
				t.Fatalf("internship runs altered campus content, version or verification: %v", err)
			}
		})
	}
}
