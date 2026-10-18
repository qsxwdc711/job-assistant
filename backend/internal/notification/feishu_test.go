package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestChannelEncryptionAndStrictWebhook(t *testing.T) {
	cipher, err := newCipher(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	secret := ChannelSecret{Webhook: "https://open.feishu.cn/open-apis/bot/v2/hook/11111111-2222-3333-4444-555555555555", Secret: "private-signing-key"}
	data, err := cipher.seal("owner-a", secret)
	if err != nil || bytes.Contains(data, []byte(secret.Secret)) || bytes.Contains(data, []byte(secret.Webhook)) {
		t.Fatal("channel not encrypted")
	}
	decoded, err := cipher.open("owner-a", data)
	if err != nil || decoded != secret {
		t.Fatal("channel roundtrip failed")
	}
	if _, err := cipher.open("owner-b", data); err == nil {
		t.Fatal("ciphertext moved across users")
	}
	data[len(data)-1] ^= 1
	if _, err := cipher.open("owner-a", data); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	for _, raw := range []string{"http://open.feishu.cn/open-apis/bot/v2/hook/abc", "https://open.feishu.cn.evil.test/open-apis/bot/v2/hook/abc", "https://user@open.feishu.cn/open-apis/bot/v2/hook/abc", "https://open.feishu.cn:443/open-apis/bot/v2/hook/abc", secret.Webhook + "?redirect=elsewhere", secret.Webhook + "#fragment", "https://127.0.0.1/private"} {
		if validWebhook(raw) {
			t.Fatalf("unsafe webhook accepted: %s", raw)
		}
	}
}

type routeTransport struct{ target string }

func (r routeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	endpoint := *request.URL
	target, err := http.NewRequest("POST", r.target, nil)
	if err != nil {
		return nil, err
	}
	endpoint.Scheme, endpoint.Host = target.URL.Scheme, target.URL.Host
	copy.URL = &endpoint
	return http.DefaultTransport.RoundTrip(copy)
}

func TestFeishuBusinessCodesAndUnknownDelivery(t *testing.T) {
	for _, test := range []struct {
		name, body, state string
		status            int
	}{{"success", `{"code":0}`, "sent", 200}, {"business_rejection", `{"code":19024,"msg":"reject"}`, "retry", 200}, {"rate_limit", `{"code":11232}`, "retry", 429}, {"missing_code", `{}`, "delivery_unknown", 200}, {"broken_response", `not json`, "delivery_unknown", 200}, {"server_ambiguous", `upstream unavailable`, "delivery_unknown", 500}} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				if _, err := io.WriteString(w, test.body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			sender := NewSender(&http.Client{Transport: routeTransport{target: server.URL}, Timeout: time.Second})
			outcome := sender.Send(context.Background(), ChannelSecret{Webhook: "https://open.feishu.cn/open-apis/bot/v2/hook/11111111-2222-3333-4444-555555555555", Secret: "test-secret"}, "求职助手：隔离测试", time.Unix(1700000000, 0))
			if outcome.State != test.state {
				t.Fatalf("got %s want %s", outcome.State, test.state)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	defer server.Close()
	sender := NewSender(&http.Client{Transport: routeTransport{target: server.URL}, Timeout: 40 * time.Millisecond})
	result := sender.Send(context.Background(), ChannelSecret{Webhook: "https://open.feishu.cn/open-apis/bot/v2/hook/11111111-2222-3333-4444-555555555555"}, "timeout", time.Now())
	if result.State != "delivery_unknown" {
		t.Fatalf("timeout retried: %#v", result)
	}
}

func TestDigestBudgetAndUntrustedMentions(t *testing.T) {
	var items []Item
	for i := 0; i < 15; i++ {
		items = append(items, Item{JobID: strconv.Itoa(i), Company: "公司<at user_id=\"all\">", Title: strings.Repeat("中文岗位", 400), Locations: []string{"深圳市"}, OfficialURL: "https://join.qq.com/" + strings.Repeat("a", 1600), DetailsURL: "http://app.test/jobs/id"})
	}
	text := Render(items, time.Now(), false)
	if strings.Contains(text, "<at") || !strings.Contains(text, "站内") || !strings.Contains(text, "共 15 个机会") || !strings.Contains(text, "另有") {
		t.Fatal("unsafe mention or missing remainder link")
	}
	message, err := encodeMessage(text, "secret", time.Now())
	if err != nil || len(message) > 18*1024 {
		t.Fatalf("UTF8 JSON budget: %d %v", len(message), err)
	}
	var value map[string]any
	if err := json.Unmarshal(message, &value); err != nil || value["sign"] == nil {
		t.Fatal("signed payload invalid")
	}
}

func TestFeishuSignatureVector(t *testing.T) {
	// Fixed independently with Python's hmac and hashlib, using Feishu's empty message.
	data, err := encodeMessage("求职助手", "test-secret", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Timestamp string `json:"timestamp"`
		Sign      string `json:"sign"`
	}
	if err = json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Timestamp != "1700000000" || payload.Sign != "mbm4Y4oluIPQ00qlBIhX8vAZ0EKv3nw0LuTb91jPL84=" {
		t.Fatalf("incorrect Feishu signature: %#v", payload)
	}
}
