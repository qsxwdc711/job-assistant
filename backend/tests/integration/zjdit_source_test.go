package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/ingest"
)

func zjditSourceServer(t *testing.T, mode string) *httptest.Server {
	t.Helper()
	data, err := os.ReadFile("../../internal/ingest/testdata/zjdit/jobs.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []map[string]any
	if err := json.Unmarshal(data, &samples); err != nil {
		t.Fatal(err)
	}
	items := samples[:2] // The actual software-test and C++ campus advertisements.
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/position/index.html" {
			if _, err := fmt.Fprint(w, `<title>四川中久数创科技有限公司招聘</title>`); err != nil {
				t.Error(err)
			}
			return
		}
		if r.URL.Path == "/Position/loading_list_data.html" {
			if err := r.ParseForm(); err != nil || r.Form.Get("class_id") != "1" || r.Form.Get("limit") != "15" {
				t.Error("wrong public list form")
			}
			jobs := items
			if mode == "empty" {
				jobs = []map[string]any{}
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"code": 200, "count": len(jobs), "data": jobs}); err != nil {
				t.Error(err)
			}
			return
		}
		if r.URL.Path != "/Position/details.html" {
			w.WriteHeader(404)
			return
		}
		id := r.URL.Query().Get("id")
		if id != "78" && id != "56" {
			t.Error("unexpected public detail identity")
			w.WriteHeader(404)
			return
		}
		body, err := os.ReadFile("../../internal/ingest/testdata/zjdit/detail-" + id + ".html")
		if err != nil {
			t.Error(err)
			return
		}
		if mode == "failure" && id == "56" {
			body = []byte(strings.Replace(string(body), "登录投递", "已结束", 1))
		}
		if _, err := w.Write(body); err != nil {
			t.Error(err)
		}
	}))
}

func TestZJDITPublicationRepeatsFailuresAndOtherSourceIsolation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := publishEvidence(t, f, evidenceJob())
	otherFilter := catalog.Filter{Company: "腾讯", IncludeUnknown: true, Limit: 20}
	before, err := store.Jobs(ctx, f.admin.ID, otherFilter, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	collect := func(mode string) ingest.Result {
		server := zjditSourceServer(t, mode)
		defer server.Close()
		reader := ingest.NewReader()
		reader.ZJDITBase = server.URL
		return ingest.Collect(ctx, store, reader, "zjdit-careers")
	}
	for range 2 {
		if result := collect("success"); result.Status != "succeeded" || result.Fetched != 2 || result.Published != 2 {
			t.Fatalf("ZJDIT collection: %#v", result)
		}
	}
	var total, version int
	if err := f.pool.QueryRow(ctx, "SELECT count(*),max(version) FROM jobs WHERE source_code='zjdit-careers'").Scan(&total, &version); err != nil || total != 2 || version != 1 {
		t.Fatalf("repeat changed identity/version: %d %d %v", total, version, err)
	}
	if result := collect("failure"); result.Status != "failed" || result.ErrorCode != "schema_changed" {
		t.Fatalf("late detail failure was published: %#v", result)
	}
	if result := collect("empty"); result.Status != "succeeded" || result.Published != 0 {
		t.Fatalf("valid empty list rejected: %#v", result)
	}
	if err := f.pool.QueryRow(ctx, "SELECT count(*),max(version) FROM jobs WHERE source_code='zjdit-careers' AND published").Scan(&total, &version); err != nil || total != 2 || version != 1 {
		t.Fatalf("failure/empty removed old content: %d %d %v", total, version, err)
	}
	after, err := store.Jobs(ctx, f.admin.ID, otherFilter, time.Now())
	if err != nil || !reflect.DeepEqual(before.Items, after.Items) {
		t.Fatalf("new source changed an existing source: %v", err)
	}
	choices, err := store.Filters(ctx, f.admin.ID, time.Now())
	if err != nil || choices.SourceNames["zjdit-careers"] != "中久数创官方技术招聘" {
		t.Fatalf("dynamic source name missing: %v", err)
	}
}
