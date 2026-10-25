package integration

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
	"time"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/ingest"
)

func requestedFixture(t *testing.T, filename string) map[string]any {
	t.Helper()
	f, err := os.Open("../../internal/ingest/testdata/" + filename + ".json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.UseNumber()
	var result map[string]any
	if err := d.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRequestedSourcesRegistryPauseAndPermissions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := catalog.NewStore(f.pool)
	expected := map[string]string{"voyah-campus": "voyah", "weride-campus": "weride", "kingnet-campus": "kingnet", "ourpalm-campus": "ourpalm", "ztgame-campus": "ztgame", "sungrow-campus": "sungrow", "37games-campus": "37games", "funplus-campus": "funplus", "bosch-campus": "bosch", "ey-campus": "ey", "bambulab-careers": "bambulab", "nvidia-careers": "nvidia", "miniso-campus": "miniso", "mxbc-campus": "mxbc", "agibot-campus": "agibot", "insta360-campus": "insta360"}
	expected["cambricon-campus"] = "cambricon"
	sources, err := store.SystemSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]catalog.Source{}
	for _, source := range sources {
		seen[source.Code] = source
	}
	for code, company := range expected {
		source, ok := seen[code]
		if !ok || source.CompanyCode != company || !ingest.SupportsSource(code) || source.Enabled {
			t.Fatalf("missing, unsupported or enabled before acceptance: %s %#v", code, source)
		}
		reader := ingest.NewReader()
		if result := ingest.Collect(ctx, store, reader, code); result.ErrorCode != "source_unavailable" || result.Requests != 0 {
			t.Fatalf("paused source fetched: %#v", result)
		}
	}
	for _, code := range []string{"tencent-campus", "tencent-internships", "huawei-campus", "huawei-internships", "cmb-campus", "nsfocus-campus", "shokz-campus", "bluepoch-careers", "zjdit-careers", "chaitin-careers"} {
		if source, ok := seen[code]; !ok || !source.Enabled || !ingest.SupportsSource(code) {
			t.Fatalf("existing source changed: %s", code)
		}
	}
	for _, code := range []string{"https://example.com", "unknown-careers", "bambulab-careers/../x"} {
		if ingest.SupportsSource(code) {
			t.Fatal("arbitrary source accepted", code)
		}
	}
	standard := f.user(t, "requested-source-reader", identity.RoleStandard, nil)
	if err := store.SetSourceEnabled(ctx, standard.ID, "bambulab-careers", true, "denied", time.Now()); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("ordinary user enabled collection", err)
	}
	if _, err := store.Sources(ctx, standard.ID, time.Now()); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("ordinary user read private diagnostics", err)
	}
}

// requestedPublicServer exercises the real production dispatch and converters
// against sanitized public jobs, including a failure after an earlier good detail.
func requestedPublicServer(t *testing.T, source, mode string) *httptest.Server {
	t.Helper()
	write := func(w http.ResponseWriter, value any) {
		t.Helper()
		if err := json.NewEncoder(w).Encode(value); err != nil {
			t.Error(err)
		}
	}
	var details []map[string]any
	switch source {
	case "bambulab-careers":
		details = []map[string]any{requestedFixture(t, "feishu/bambulab-frontend"), requestedFixture(t, "feishu/bambulab-backend-intern")}
	case "nvidia-careers":
		details = []map[string]any{requestedFixture(t, "workday/nvidia-JR2026546"), requestedFixture(t, "workday/nvidia-JR2026972")}
	case "miniso-campus":
		for _, id := range []string{"1f528032-29cd-4384-93de-ef66e6c96a9f", "b336d5cd-5a95-45f2-ac23-bc3159c86a42"} {
			details = append(details, requestedFixture(t, "beisen/miniso-detail-"+id)["Data"].(map[string]any))
		}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch source {
		case "bambulab-careers":
			if r.URL.Path == "/campus/position/list" {
				if _, err := fmt.Fprint(w, `<script id="js-websiteInfo">{"tenant_info":{"tenant_name":"Bambulab"},"website_info":{"id":"7036539082735274248","path":"campus"}}</script>`); err != nil {
					t.Error(err)
				}
				return
			}
			if r.URL.Path == "/api/v1/csrf/token" {
				write(w, map[string]any{"code": 0, "data": map[string]any{"token": "fictional-public-session"}})
				return
			}
			if r.URL.Path == "/api/v1/search/job/posts" {
				list := details
				if mode == "empty" {
					list = []map[string]any{}
				}
				write(w, map[string]any{"code": 0, "data": map[string]any{"count": len(list), "job_post_list": list}})
				return
			}
			for i, detail := range details {
				if strings.HasSuffix(r.URL.Path, "/"+fmt.Sprint(detail["id"])) {
					if mode == "failure" && i == 1 {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					if mode == "metadata" {
						detail["publish_time"] = json.Number("1792999999999")
					}
					write(w, map[string]any{"code": 0, "data": map[string]any{"job_post_detail": detail}})
					return
				}
			}
		case "nvidia-careers":
			if strings.HasSuffix(r.URL.Path, "/jobs") {
				var input struct {
					AppliedFacets struct {
						WorkerSubType []string `json:"workerSubType"`
					} `json:"appliedFacets"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				list := []any{}
				facets := []any{}
				if len(input.AppliedFacets.WorkerSubType) == 1 && input.AppliedFacets.WorkerSubType[0] == "0c40f6bd1d8f10adf6dae42e46d44a17" && mode != "empty" {
					original := requestedFixture(t, "workday/nvidia-intern-list")["jobPostings"].([]any)
					list = original[:2]
					facets = []any{map[string]any{"facetParameter": "workerSubType", "values": []any{map[string]any{"id": "0c40f6bd1d8f10adf6dae42e46d44a17", "descriptor": "Intern (Fixed Term)", "count": 2}}}, map[string]any{"facetParameter": "locationHierarchy1", "values": []any{map[string]any{"id": "2fcb99c455831013ea529fe151e3323c", "descriptor": "China", "count": 2}}}}
				}
				write(w, map[string]any{"total": len(list), "jobPostings": list, "facets": facets})
				return
			}
			for i, detail := range details {
				info := detail["jobPostingInfo"].(map[string]any)
				if strings.HasSuffix(r.URL.Path, "_"+fmt.Sprint(info["jobReqId"])) {
					if mode == "failure" && i == 1 {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					if mode == "metadata" {
						info["postedOn"] = "Posted Yesterday"
						info["startDate"] = "2026-10-23"
					}
					write(w, detail)
					return
				}
			}
		case "miniso-campus":
			if r.URL.Path == "/campus/jobs" {
				if _, err := fmt.Fprint(w, `<script>var BSGlobal = {"tenantInfo":{"Id":109979,"Alias":"名创优品","Status":1}};</script>`); err != nil {
					t.Error(err)
				}
				return
			}
			if r.URL.Path == "/api/Jobad/GetJobAdPageList" {
				list := details
				if mode == "empty" {
					list = []map[string]any{}
				}
				write(w, map[string]any{"Code": 200, "Count": len(list), "Data": list})
				return
			}
			if r.URL.Path == "/api/JobAd/GetJobAdInfo" {
				for i, detail := range details {
					if r.URL.Query().Get("jobAdId") == fmt.Sprint(detail["Id"]) {
						if mode == "failure" && i == 1 {
							w.WriteHeader(http.StatusForbidden)
							return
						}
						if mode == "metadata" {
							detail["PostDate"] = "2026-10-23"
						}
						write(w, map[string]any{"Code": 200, "Data": detail})
						return
					}
				}
			}
		}
		t.Error("unexpected public request", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
}

func TestRequestedAdaptersPublishRepeatLateFailureAndIsolation(t *testing.T) {
	for _, source := range []string{"bambulab-careers", "nvidia-careers", "miniso-campus"} {
		t.Run(source, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			store := publishEvidence(t, f, evidenceJob())
			standard := f.user(t, "new-source-reader", identity.RoleStandard, nil)
			filter := catalog.Filter{Company: "腾讯", IncludeUnknown: true, Limit: 20}
			before, err := store.Jobs(ctx, standard.ID, filter, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetSourceEnabled(ctx, f.admin.ID, source, true, "accepted-test-source", time.Now()); err != nil {
				t.Fatal(err)
			}
			collect := func(mode string) ingest.Result {
				server := requestedPublicServer(t, source, mode)
				defer server.Close()
				r := ingest.NewReader()
				r.FeishuBase = server.URL
				r.WorkdayBase = server.URL
				r.BeisenBase = server.URL
				return ingest.Collect(ctx, store, r, source)
			}
			for _, mode := range []string{"success", "success", "metadata"} {
				result := collect(mode)
				if result.Status != "succeeded" || result.Fetched != 2 || result.Published != 2 {
					t.Fatalf("complete publication %s: %#v", mode, result)
				}
			}
			if result := collect("failure"); result.Status != "restricted" || result.Published != 0 {
				t.Fatalf("late failure partially published: %#v", result)
			}
			var count, version int
			if err := f.pool.QueryRow(ctx, "SELECT count(*),max(version) FROM jobs WHERE source_code=$1 AND published", source).Scan(&count, &version); err != nil || count != 2 || version != 1 {
				t.Fatalf("repeat/failure changed content: %d %d %v", count, version, err)
			}
			if err := store.SetSourceEnabled(ctx, f.admin.ID, source, false, "pause-test-source", time.Now()); err != nil {
				t.Fatal(err)
			}
			if result := ingest.Collect(ctx, store, ingest.NewReader(), source); result.Requests != 0 || result.ErrorCode != "source_unavailable" {
				t.Fatalf("paused source fetched: %#v", result)
			}
			after, err := store.Jobs(ctx, standard.ID, filter, time.Now())
			if err != nil || !reflect.DeepEqual(before.Items, after.Items) {
				t.Fatal("changed an existing source", err)
			}
		})
	}
}
