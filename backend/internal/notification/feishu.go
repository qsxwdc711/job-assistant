package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

// MaxMessageBytes leaves headroom below Feishu's documented 20 KiB limit.
const MaxMessageBytes = 18 * 1024

// Outcome classifies one attempt using stable, credential-free status codes.
type Outcome struct {
	State, Code string
	Bytes       int
}

// Sender performs one attempt. The persistent outbox owns all retry decisions.
type Sender struct{ client *http.Client }

// NewSender copies the client, bounds requests and forbids redirects.
func NewSender(client *http.Client) *Sender {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if clientCopy.Timeout == 0 || clientCopy.Timeout > 8*time.Second {
		clientCopy.Timeout = 8 * time.Second
	}
	return &Sender{client: &clientCopy}
}

func encodeMessage(text, secret string, now time.Time) ([]byte, error) {
	payload := map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
	if secret != "" {
		timestamp := strconv.FormatInt(now.Unix(), 10)
		mac := hmac.New(sha256.New, []byte(timestamp+"\n"+secret))
		payload["timestamp"], payload["sign"] = timestamp, base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	return json.Marshal(payload)
}

// Send attempts one signed message; an ambiguous response never requests a retry.
func (s *Sender) Send(ctx context.Context, secret ChannelSecret, text string, now time.Time) Outcome {
	if !validWebhook(secret.Webhook) {
		return Outcome{State: "failed", Code: "invalid_channel"}
	}
	data, err := encodeMessage(text, secret.Secret, now)
	if err != nil || len(data) > MaxMessageBytes {
		return Outcome{State: "failed", Code: "message_too_large"}
	}
	result := Outcome{State: "delivery_unknown", Code: "network_ambiguous", Bytes: len(data)}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, secret.Webhook, bytes.NewReader(data))
	if err != nil {
		result.State, result.Code = "failed", "invalid_channel"
		return result
	}
	request.Header.Set("Content-Type", "application/json;charset=UTF-8")
	response, err := s.client.Do(request)
	if err != nil {
		return result
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		result.State, result.Code = "retry", "feishu_rate_limited"
		return result
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 16*1024))
	if err != nil {
		return result
	}
	var business struct {
		Code *int `json:"code"`
	}
	if err = json.Unmarshal(body, &business); err != nil || business.Code == nil {
		result.Code = "response_ambiguous"
		return result
	}
	if response.StatusCode == http.StatusOK && *business.Code == 0 {
		result.State, result.Code = "sent", ""
		return result
	}
	if *business.Code != 0 {
		result.State, result.Code = "retry", "feishu_business_"+strconv.Itoa(*business.Code)
		return result
	}
	result.Code = "response_ambiguous"
	return result
}
