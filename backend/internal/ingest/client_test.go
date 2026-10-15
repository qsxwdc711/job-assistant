package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicRestrictionDoesNotRetry(t *testing.T) {
	for _, status := range []int{401, 403, 429} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		reader := NewReader()
		_, err := reader.read(context.Background(), http.MethodGet, server.URL, nil, nil)
		server.Close()
		if !errors.Is(err, ErrRestricted) || reader.Requests != 1 {
			t.Fatalf("restricted %d: %v, requests %d", status, err, reader.Requests)
		}
	}
}

func TestTencentRejectsBusinessErrorAndIncompletePages(t *testing.T) {
	for _, mode := range []string{"business_error", "truncated", "duplicate"} {
		page := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "getProjectMapping") {
				writeFixture(t, w, "tencent-mapping.json")
				return
			}
			page++
			entries := []any{fixture(t, "tencent-list-entry.json")}
			if mode == "truncated" && page > 1 {
				entries = nil
			}
			status := 0
			if mode == "business_error" {
				status = 1
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"status": status, "data": map[string]any{"count": 2, "positionList": entries}}); err != nil {
				t.Error(err)
			}
		}))
		reader := NewReader()
		reader.TencentBase = server.URL
		docs, _, err := reader.Tencent(context.Background())
		server.Close()
		if !errors.Is(err, ErrSchema) || len(docs) != 0 {
			t.Fatalf("%s accepted partial source: %d, %v", mode, len(docs), err)
		}
	}
}

func TestPublicReaderRejectsUnexpectedRedirectAndOversizedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://example.invalid/private", http.StatusFound)
			return
		}
		if _, err := w.Write([]byte(strings.Repeat("x", 4*1024*1024+1))); err != nil {
			return
		}
	}))
	defer server.Close()
	reader := NewReader()
	if _, err := reader.read(context.Background(), http.MethodGet, server.URL+"/redirect", nil, nil); !errors.Is(err, ErrRestricted) {
		t.Fatalf("unexpected redirect: %v", err)
	}
	if _, err := reader.read(context.Background(), http.MethodGet, server.URL+"/oversized", nil, nil); err == nil {
		t.Fatal("oversized public body accepted")
	}
}
