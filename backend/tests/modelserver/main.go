// This executable is an isolated QA provider, never included in the production image.
package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/profile"
)

func main() {
	if os.Getenv("S3_ISOLATED_MODEL") != "true" {
		slog.Error("isolated model requires explicit test configuration")
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("POST /chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 512*1024)
		if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 2 {
			w.WriteHeader(400)
			return
		}
		var input map[string]string
		if json.Unmarshal([]byte(request.Messages[1].Content), &input) != nil {
			w.WriteHeader(400)
			return
		}
		for _, pii := range []string{"张三", "13800138000", "synthetic@example.com", "110101199001011234", "北京市海淀区测试路1号"} {
			if strings.Contains(input["resume"], pii) {
				slog.Error("synthetic identity reached isolated provider")
				w.WriteHeader(400)
				return
			}
		}
		if !strings.Contains(input["resume"], "Go") {
			w.WriteHeader(400)
			return
		}
		result := any(profile.Candidate{Education: "bachelor", EducationRegion: "unknown", Skills: []string{"Go"}, Evidence: []profile.Evidence{{Field: "education", Quote: "本科"}, {Field: "skills", Quote: "使用 Go 开发接口"}}})
		if input["jd"] != "" {
			result = matching.Semantic{Level: "medium", Required: []matching.Citation{{Statement: "合成资料包含 Go 接口实践", ResumeQuote: "使用 Go 开发接口", JDQuote: "Go"}}, Gaps: []matching.Citation{{Statement: "资料中未找到网络协议实践证据，需进一步确认", JDQuote: "TCP/UDP"}}}
		}
		raw, err := json.Marshal(result)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": string(raw)}, "finish_reason": "stop"}}}); err != nil {
			slog.Warn("test provider response interrupted")
		}
	})
	server := &http.Server{Addr: ":8090", Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 20 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		slog.Error("isolated model server stopped")
		os.Exit(1)
	}
}
