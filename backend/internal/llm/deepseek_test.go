package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"jobassistant/backend/internal/profile"
)

func TestStrictJSONAndProviderFailures(t *testing.T) {
	for _, tc := range []struct {
		name, content, finish string
		status                int
		want                  error
	}{
		{"empty", "", "stop", 200, profile.ErrInvalidOutput},
		{"truncated", `{"skills":["Go"]}`, "length", 200, profile.ErrInvalidOutput},
		{"unknown_field", `{"invented":true}`, "stop", 200, profile.ErrInvalidOutput},
		{"extra_json", `{} {}`, "stop", 200, profile.ErrInvalidOutput},
		{"hallucinated_skill", `{"skills":["Rust"],"evidence":[{"field":"skills","quote":"Go"}]}`, "stop", 200, profile.ErrInvalidOutput},
		{"rate_limit", "", "", 429, ErrTemporary}, {"outage", "", "", 503, ErrTemporary}, {"invalid_key", "", "", 401, ErrRejected},
		{"valid", `{"skills":["Go"],"evidence":[{"field":"skills","quote":"使用 Go 开发接口"}]}`, "stop", 200, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if tc.status == 200 {
					if err := json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message{Role: "assistant", Content: tc.content}, "finish_reason": tc.finish}}}); err != nil {
						t.Error(err)
					}
				}
			}))
			defer server.Close()
			client, err := New(Options{BaseURL: server.URL, Model: "test", APIKey: "fake", AllowHTTP: true})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_, err = client.Parse(context.Background(), "使用 Go 开发接口")
			if !errors.Is(err, tc.want) {
				t.Fatalf("Parse error=%v want=%v", err, tc.want)
			}
		})
	}
}
func TestConfigurationAndNoSilentFakeFallback(t *testing.T) {
	if _, err := New(Options{BaseURL: "http://provider.invalid"}); err == nil {
		t.Fatal("HTTP allowed by default")
	}
	a, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.Configured() {
		t.Fatal("keyless model configured")
	}
	if _, err = a.Parse(context.Background(), "Go"); !errors.Is(err, profile.ErrUnconfigured) {
		t.Fatal(err)
	}
	b, err := New(Options{APIKey: "a-secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if a.Version() != b.Version() {
		t.Fatal("cache version contains credential state")
	}
	c, err := New(Options{Model: "another-configured-model"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Version() == a.Version() {
		t.Fatal("model configuration not versioned")
	}
}
