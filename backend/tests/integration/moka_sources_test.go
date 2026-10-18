package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"jobassistant/backend/internal/application"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/httpapi"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/ingest"
)

func mokaSourceServer(t *testing.T, source, mode string) *httptest.Server {
	t.Helper()
	var org, site, path, kind, filename string
	switch source {
	case "nsfocus-campus":
		org, site, path, kind, filename = "nsfocus", "29118", "/campus_apply/nsfocus/29118", "campus", "nsfocus-security"
	case "shokz-campus":
		org, site, path, kind, filename = "aftershokzhr", "36940", "/campus-recruitment/aftershokzhr/36940", "campus", "shokz-ai-intern"
	case "bluepoch-careers":
		org, site, path, kind, filename = "blueinteractive", "38433", "/apply/blueinteractive/38433", "social", "bluepoch-experienced-java"
	default:
		t.Fatal("unrecognized Moka test source")
	}
	raw, err := os.ReadFile("../../internal/ingest/testdata/moka/" + filename + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var detail map[string]any
	if err = json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == path {
			data, err := json.Marshal(map[string]any{"org": map[string]any{"id": org}, "siteId": site, "mode": kind, "aesIv": "0123456789abcdef"})
			if err != nil {
				t.Error(err)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "anonymous_fixture", Value: "public-only", Path: "/"})
			if _, err = fmt.Fprintf(w, `<input id="init-data" value="%s">`, html.EscapeString(string(data))); err != nil {
				t.Error(err)
			}
			return
		}
		if cookie, err := r.Cookie("anonymous_fixture"); err != nil || cookie.Value != "public-only" {
			t.Error("anonymous cookie not retained")
			w.WriteHeader(403)
			return
		}
		var query map[string]any
		if err := json.NewDecoder(r.Body).Decode(&query); err != nil || query["orgId"] != org || query["siteId"] != site {
			t.Error("wrong public query tenant")
			w.WriteHeader(400)
			return
		}
		var data any
		if r.URL.Path == "/api/outer/ats-apply/website/job" {
			value := map[string]any{}
			for k, v := range detail {
				value[k] = v
			}
			if mode == "failure" {
				value["id"] = "ffffffff-ffff-4fff-8fff-ffffffffffff"
			}
			data = value
		} else {
			jobs, total := []any{detail}, 1
			if mode == "empty" {
				jobs, total = []any{}, 0
			}
			data = map[string]any{"jobStats": map[string]any{"orgId": org, "total": total}, "jobs": jobs}
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"success": true, "code": 0, "data": data}); err != nil {
			t.Error(err)
		}
	}))
}

func TestMokaSourcePublicationRepeatFailureAndExistingIsolation(t *testing.T) {
	for _, source := range []string{"nsfocus-campus", "shokz-campus", "bluepoch-careers"} {
		t.Run(source, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			store := publishEvidence(t, f, evidenceJob())
			originalFilter := catalog.Filter{Company: "腾讯", Limit: 20, IncludeUnknown: true}
			before, err := store.Jobs(ctx, f.admin.ID, originalFilter, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			collect := func(mode string) ingest.Result {
				server := mokaSourceServer(t, source, mode)
				defer server.Close()
				reader := ingest.NewReader()
				reader.MokaBase = server.URL
				return ingest.Collect(ctx, store, reader, source)
			}
			want := 1
			if source == "bluepoch-careers" {
				want = 0
			}
			for range 2 {
				result := collect("success")
				if result.Status != "succeeded" || result.Fetched != 1 || result.Published != want {
					t.Fatalf("Moka public collection: %#v", result)
				}
			}
			var total, version int
			if err = f.pool.QueryRow(ctx, "SELECT count(*),COALESCE(max(version),0) FROM jobs WHERE source_code=$1", source).Scan(&total, &version); err != nil || total != want || (want > 0 && version != 1) {
				t.Fatalf("Moka stable publication/version: %d %d %v", total, version, err)
			}
			if result := collect("failure"); result.Status != "failed" || result.ErrorCode != "schema_changed" {
				t.Fatalf("bad detail published: %#v", result)
			}
			if result := collect("empty"); result.Status != "succeeded" || result.Published != 0 {
				t.Fatalf("valid empty: %#v", result)
			}
			var retained int
			if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE source_code=$1 AND published", source).Scan(&retained); err != nil || retained != want {
				t.Fatalf("failure/empty removed jobs: %d %v", retained, err)
			}
			after, err := store.Jobs(ctx, f.admin.ID, originalFilter, time.Now().UTC())
			if err != nil || !reflect.DeepEqual(before.Items, after.Items) {
				t.Fatalf("Moka changed another source: %v", err)
			}
			choices, err := store.Filters(ctx, f.admin.ID, time.Now().UTC())
			if err != nil || choices.SourceNames[source] == "" {
				t.Fatalf("new source name unavailable: %v", err)
			}
		})
	}
}

func TestMokaPublicNamesDoNotExposeAdministratorDiagnostics(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	user := f.user(t, "source-name-reader", identity.RoleStandard, nil)
	store := catalog.NewStore(f.pool)
	choices, err := store.Filters(ctx, user.ID, time.Now())
	if err != nil || len(choices.SourceNames) < 8 || choices.SourceNames["nsfocus-campus"] == "" {
		t.Fatalf("ordinary source choices: %v", err)
	}
	handler, err := httpapi.NewHandler(f.service, httpapi.Config{CSRFKey: bytes.Repeat([]byte{3}, 32), Origin: "http://app.test"}, f.pool.Ping, httpapi.WithRecruitment(store, application.NewStore(f.pool)))
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	server := httptest.NewServer(handler)
	defer server.Close()
	browser := newBrowser(t, server)
	browser.login(t, "source-name-reader")
	status, data, _ := browser.request(t, "GET", "/api/v1/jobs/filters", nil, false)
	if status != 200 || !bytes.Contains(data, []byte("source_names")) || bytes.Contains(data, []byte("last_error")) {
		t.Fatalf("public source names leaked diagnostics: %d", status)
	}
	if status, _, _ = browser.request(t, "GET", "/api/v1/admin/sources", nil, false); status != 403 {
		t.Fatalf("ordinary diagnostics access: %d", status)
	}
}

func TestMokaSourcesShareDomainAndGlobalReadLimits(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := catalog.NewStore(f.pool)
	var releases []func() error
	t.Cleanup(func() {
		for _, release := range releases {
			if err := release(); err != nil {
				t.Error(err)
			}
		}
	})
	for _, source := range []string{"tencent-campus", "huawei-campus", "cmb-campus"} {
		release, err := store.CollectionLease(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, err := store.CollectionLease(ctx, "nsfocus-campus"); !errors.Is(err, catalog.ErrCollectionBusy) {
		t.Fatalf("fourth domain bypassed three-reader limit: %v", err)
	}
	if err := releases[2](); err != nil {
		t.Fatal(err)
	}
	releases = releases[:2]
	release, err := store.CollectionLease(ctx, "nsfocus-campus")
	if err != nil {
		t.Fatal(err)
	}
	releases = append(releases, release)
	result := ingest.Collect(ctx, store, ingest.NewReader(), "shokz-campus")
	if result.ErrorCode != "source_busy" || result.Requests != 0 {
		t.Fatalf("same-host tenant reached public HTTP: %#v", result)
	}
}
