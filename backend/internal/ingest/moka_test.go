package ingest

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const mokaTestIV = "0123456789abcdef"

func mokaEnvelope(t *testing.T, value any) []byte {
	t.Helper()
	plain, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(padding)}, padding)...)
	block, err := aes.NewCipher([]byte("fictionalkey1234"))
	if err != nil {
		t.Fatal(err)
	}
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, []byte(mokaTestIV)).CryptBlocks(encrypted, plain)
	body, err := json.Marshal(map[string]any{"necromancer": "fictionalkey1234", "data": base64.StdEncoding.EncodeToString(encrypted)})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func mokaHome(t *testing.T, site mokaSite) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"org": map[string]any{"id": site.org}, "siteId": site.site, "mode": site.mode, "aesIv": mokaTestIV})
	if err != nil {
		t.Fatal(err)
	}
	return `<input value="` + html.EscapeString(string(body)) + `" id="init-data">`
}

func mokaWrite(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(mokaEnvelope(t, map[string]any{"success": true, "code": 0, "data": data})); err != nil {
		t.Error(err)
	}
}

func TestMokaVerifiedTenantsCookieEnvelopeAndScope(t *testing.T) {
	for _, sample := range []struct{ source, file, kind string }{{"nsfocus-campus", "nsfocus-security", "campus"}, {"shokz-campus", "shokz-ai-intern", "internship"}, {"shokz-campus", "shokz-master-intern", "internship"}, {"shokz-campus", "shokz-joint-topic", "excluded"}, {"bluepoch-careers", "bluepoch-experienced-java", "excluded"}} {
		t.Run(sample.file, func(t *testing.T) {
			site, _ := mokaConfig(sample.source)
			detail := object(fixture(t, "moka/"+sample.file+".json"))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path == site.path {
					http.SetCookie(w, &http.Cookie{Name: "isolated_public_session", Value: "anonymous-only", Path: "/"})
					if _, err := fmt.Fprint(w, mokaHome(t, site)); err != nil {
						t.Error(err)
					}
					return
				}
				cookie, err := request.Cookie("isolated_public_session")
				if err != nil || cookie.Value != "anonymous-only" || request.Method != "POST" {
					t.Error("public homepage cookie or POST query was lost")
					w.WriteHeader(403)
					return
				}
				var query map[string]any
				if err := json.NewDecoder(request.Body).Decode(&query); err != nil || query["orgId"] != site.org || query["siteId"] != site.site {
					t.Error("tenant query mismatch")
				}
				switch request.URL.Path {
				case site.endpoint:
					if query["site"] != site.mode || query["offset"] != float64(0) || query["limit"] != float64(15) {
						t.Error("list query contract changed")
					}
					mokaWrite(t, w, map[string]any{"jobStats": map[string]any{"orgId": site.org, "total": 1}, "jobs": []any{detail}})
				case "/api/outer/ats-apply/website/job":
					if query["jobId"] != detail["id"] {
						t.Error("wrong detail requested")
					}
					mokaWrite(t, w, detail)
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			reader := NewReader()
			originalClient := reader.Client
			reader.MokaBase = server.URL
			docs, total, err := reader.Moka(context.Background(), sample.source)
			if err != nil || total != 1 || reader.Client != originalClient || reader.Client.Jar != nil {
				t.Fatalf("public tenant read/lifetime: total=%d err=%v", total, err)
			}
			if sample.kind == "excluded" {
				if len(docs) != 0 || len(reader.Exclusions) != 1 {
					t.Fatal("experienced social job entered campus/low-experience scope")
				}
				return
			}
			if len(docs) != 1 || docs[0].RecruitmentType != sample.kind || docs[0].ExternalID != text(detail["id"]) || docs[0].WrittenTest != "unknown" || docs[0].DeadlineAt != nil || len(docs[0].GraduationRules) != 0 {
				t.Fatalf("normalization boundary: %#v", docs)
			}
			if !strings.HasSuffix(docs[0].ApplyURL, "#/job/"+text(detail["id"])) || docs[0].ApplyLevel != "job" {
				t.Fatal("official detail route was replaced")
			}
			if sample.file == "shokz-ai-intern" && (docs[0].MinimumInternshipMonths == nil || *docs[0].MinimumInternshipMonths != 3 || docs[0].StudentRequired != "yes") {
				t.Fatal("explicit internship conditions lost")
			}
			if sample.file == "shokz-master-intern" && (!strings.Contains(docs[0].Variants[0].Requirements, "官方学历字段：硕士及以上") || docs[0].StudentRequired != "yes" || docs[0].MinimumInternshipMonths != nil) {
				t.Fatal("master's requirement or unknown duration changed")
			}
		})
	}
}

func TestMokaPublicEnvelopeValidation(t *testing.T) {
	valid := mokaEnvelope(t, map[string]any{"success": true, "code": 0, "data": map[string]any{"value": "中文公开岗位"}})
	if value, err := mokaDecode(valid, mokaTestIV); err != nil || value["value"] != "中文公开岗位" {
		t.Fatalf("public response decode: %v", err)
	}
	for _, body := range [][]byte{[]byte(`{}`), []byte(`{"code":0,"success":true,"data":null}`), []byte(`{"success":true,"code":0,"data":{}} {}`), []byte(`{"necromancer":"short","data":"AA=="}`), []byte(`{"necromancer":"fictionalkey1234","data":"not base64"}`), []byte(`{"necromancer":"fictionalkey1234","data":"AAAAAAAAAAAAAAAAAAAAAA=="}`)} {
		if _, err := mokaDecode(body, mokaTestIV); !errors.Is(err, ErrSchema) {
			t.Fatalf("invalid envelope accepted: %v", err)
		}
	}
	if _, err := mokaDecode(valid, "wrong IV"); !errors.Is(err, ErrSchema) {
		t.Fatal("invalid IV accepted")
	}
	if _, err := mokaDecode([]byte(`{"code":403,"success":false,"msg":"请登录"}`), mokaTestIV); !errors.Is(err, ErrRestricted) {
		t.Fatal("business restriction treated as empty success")
	}
	site, _ := mokaConfig("nsfocus-campus")
	if _, err := mokaInit([]byte(mokaHome(t, site)+mokaHome(t, site)), site); !errors.Is(err, ErrSchema) {
		t.Fatal("ambiguous init-data accepted")
	}
	other, _ := mokaConfig("shokz-campus")
	if _, err := mokaInit([]byte(mokaHome(t, other)), site); !errors.Is(err, ErrSchema) {
		t.Fatal("another tenant homepage accepted")
	}
}

func TestMokaPaginationCompletenessAndDetailIdentity(t *testing.T) {
	for _, mode := range []string{"complete", "paused", "empty", "duplicate", "changed_total", "short_page", "missing_array", "detail_identity", "detail_status", "detail_description", "business_error", "http_restricted"} {
		t.Run(mode, func(t *testing.T) {
			site, _ := mokaConfig("nsfocus-campus")
			items := []map[string]any{}
			for i := 0; i < 16; i++ {
				items = append(items, map[string]any{"id": fmt.Sprintf("%08x-0000-4000-8000-000000000000", i), "orgId": site.org, "title": "市场营销", "status": "open", "hireMode": 2, "commitment": "全职", "jobDescription": "计算机专业优先，负责市场活动"})
			}
			if strings.HasPrefix(mode, "detail_") {
				items = items[:1]
				items[0]["title"] = "软件开发工程师"
			}
			if mode == "paused" {
				items[0]["title"] = "软件开发工程师"
				items[0]["status"] = "pause"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path == site.path {
					if _, err := fmt.Fprint(w, mokaHome(t, site)); err != nil {
						t.Error(err)
					}
					return
				}
				if mode == "http_restricted" {
					w.WriteHeader(429)
					return
				}
				if mode == "business_error" {
					mokaWrite(t, w, nil)
					return
				}
				if request.URL.Path == "/api/outer/ats-apply/website/job" {
					value := map[string]any{}
					for k, v := range items[0] {
						value[k] = v
					}
					if mode == "detail_identity" {
						value["id"] = "ffffffff-0000-4000-8000-000000000000"
					} else if mode == "detail_status" {
						value["status"] = "closed"
					} else {
						delete(value, "jobDescription")
					}
					mokaWrite(t, w, value)
					return
				}
				var query struct {
					Offset int `json:"offset"`
				}
				if err := json.NewDecoder(request.Body).Decode(&query); err != nil {
					t.Error(err)
				}
				total := len(items)
				var jobs any = items[query.Offset:min(query.Offset+15, total)]
				if mode == "empty" {
					total = 0
					jobs = []any{}
				}
				if mode == "duplicate" && query.Offset > 0 {
					jobs = items[:1]
				}
				if mode == "changed_total" && query.Offset > 0 {
					total++
				}
				if mode == "short_page" && query.Offset == 0 {
					jobs = items[:14]
				}
				if mode == "missing_array" {
					total = 0
					jobs = nil
				}
				mokaWrite(t, w, map[string]any{"jobStats": map[string]any{"orgId": site.org, "total": total}, "jobs": jobs})
			}))
			defer server.Close()
			reader := NewReader()
			reader.MokaBase = server.URL
			docs, total, err := reader.Moka(context.Background(), site.source)
			switch mode {
			case "complete", "paused":
				if err != nil || total != 16 || len(docs) != 0 || len(reader.Exclusions) != 16 {
					t.Fatalf("complete nontechnical snapshot: %d %v", total, err)
				}
			case "empty":
				if err != nil || total != 0 || len(docs) != 0 {
					t.Fatalf("valid empty: %v", err)
				}
			case "http_restricted":
				if !errors.Is(err, ErrRestricted) || reader.Requests != 2 {
					t.Fatalf("restriction retried: %v %d", err, reader.Requests)
				}
			default:
				if !errors.Is(err, ErrSchema) || docs != nil {
					t.Fatalf("incomplete/foreign snapshot accepted: %v", err)
				}
			}
		})
	}
}

func TestMokaRoleAndConditionBoundaries(t *testing.T) {
	for _, title := range []string{"海外营销增长实习生-AI&GEO方向", "AI产品实习生", "GUI设计师—UE5", "声学测试工程师", "Retail运营"} {
		if mokaDirection(title) != "unknown" {
			t.Fatalf("nontechnical role accepted: %s", title)
		}
	}
	for _, sample := range []struct{ title, direction string }{{"AI 工程师实习生", "algorithm_ai"}, {"Java开发工程师", "backend"}, {"JavaScript前端工程师", "frontend"}, {"Java测试开发工程师", "test"}, {"嵌入式C++开发工程师", "embedded"}, {"安全运营算法研究员", "algorithm_ai"}, {"秋招安全研究员", "security"}} {
		if mokaDirection(sample.title) != sample.direction {
			t.Fatalf("technical role lost: %s", sample.title)
		}
	}
	site, _ := mokaConfig("bluepoch-careers")
	for _, sample := range []struct {
		body   string
		accept bool
	}{{"接受应届毕业生，3年以上开发经验优先", true}, {"接受应届毕业生，至少3年以上开发经验，熟悉 Go 优先", false}, {"1年以上工作经验", true}, {"负责开发工作", false}} {
		detail := map[string]any{"commitment": "全职", "hireMode": json.Number("1")}
		kind, reason := mokaRecruitment(site, detail, sample.body)
		if (kind == "early_career" && reason == "") != sample.accept {
			t.Fatalf("social experience boundary: %s", sample.body)
		}
	}
	place := mokaLocation(map[string]any{"country": "China", "cityId": json.Number("110108"), "cityName": "Haidian"})
	if place.City != "北京市" || place.Kind != "domestic" {
		t.Fatalf("district substituted for prefecture: %#v", place)
	}
	unknown := mokaLocation(map[string]any{"country": "China", "cityId": json.Number("999999"), "cityName": "UnknownPlace"})
	if unknown.Kind != "unknown" || unknown.City != "UnknownPlace" {
		t.Fatal("unknown place fabricated")
	}
	foreign := mokaLocation(map[string]any{"country": "United Kingdom", "cityName": "Bristol"})
	if foreign.Kind != "overseas" || foreign.City != "Bristol" {
		t.Fatal("overseas place lost")
	}
}
