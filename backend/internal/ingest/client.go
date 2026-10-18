package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"jobassistant/backend/internal/catalog"
)

var (
	// ErrRestricted marks public access that now requires authorization or a challenge.
	ErrRestricted = errors.New("public source restricted")
	// ErrSchema marks an unexpected business response or incomplete collection.
	ErrSchema = errors.New("public source schema or completeness changed")
)

// Reader bounds public requests and records each response's provenance.
type Reader struct {
	Client      *http.Client
	Evidence    []catalog.Evidence
	TencentBase string
	HuaweiBase  string
	HuaweiAPI   string
	CMBBase     string
	MokaBase    string
	ZJDITBase   string
	readSpacing time.Duration
	lastRead    time.Time
	Requests    int
	Exclusions  []catalog.Exclusion
}

// NewReader creates an anonymous reader with verified public hosts and bounded redirects.
func NewReader() *Reader {
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) > 3 || !trustedURL(request.URL) {
			return ErrRestricted
		}
		return nil
	}}
	return &Reader{Client: client, Evidence: []catalog.Evidence{}, TencentBase: "https://join.qq.com", HuaweiBase: "https://career.huawei.com", HuaweiAPI: "https://apigw-dgg-b0.huawei.com/api/apig/channelhw", CMBBase: "https://career.cmbchina.com", MokaBase: "https://app.mokahr.com", ZJDITBase: "https://zp.zjdit.com"}
}

func trustedURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Hostname() {
	case "join.qq.com", "career.huawei.com", "apigw-dgg-b0.huawei.com", "career.cmbchina.com", "app.mokahr.com", "zp.zjdit.com":
		return true
	}
	return false
}

func (r *Reader) read(ctx context.Context, method, rawURL string, input any, headers map[string]string) ([]byte, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		data, err := r.readOnce(ctx, method, rawURL, input, headers)
		if err == nil {
			return data, nil
		}
		last = err
		var timeout net.Error
		transient := errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || (errors.As(err, &timeout) && timeout.Timeout())
		if !transient || ctx.Err() != nil || attempt == 2 {
			return nil, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, last
}

func (r *Reader) readOnce(ctx context.Context, method, rawURL string, input any, headers map[string]string) ([]byte, error) {
	if wait := max(200*time.Millisecond, r.readSpacing) - time.Since(r.lastRead); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	var body io.Reader
	form, isForm := input.(url.Values)
	if isForm {
		body = strings.NewReader(form.Encode())
	} else if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "JobAssistant/0.1 (public recruitment reader)")
	request.Header.Set("Accept", "application/json,text/html;q=0.8")
	if isForm {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	} else if input != nil {
		request.Header.Set("Content-Type", "application/json;charset=UTF-8")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	r.Requests++
	response, err := r.Client.Do(request)
	r.lastRead = time.Now()
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 429 {
		return nil, ErrRestricted
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("public HTTP status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 4*1024*1024 {
		return nil, fmt.Errorf("public response exceeded size limit")
	}
	sum := sha256.Sum256(data)
	r.Evidence = append(r.Evidence, catalog.Evidence{URL: rawURL, SHA256: hex.EncodeToString(sum[:]), Bytes: len(data), FetchedAt: time.Now().UTC()})
	return data, nil
}

func (r *Reader) json(ctx context.Context, method, rawURL string, input any, headers map[string]string) (map[string]any, error) {
	data, err := r.read(ctx, method, rawURL, input, headers)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		return nil, ErrSchema
	}
	if result == nil {
		return nil, ErrSchema
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, ErrSchema
	}
	return result, nil
}

func text(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case nil:
		return ""
	default:
		return ""
	}
}
func object(value any) map[string]any { v, _ := value.(map[string]any); return v }
func array(value any) []any           { v, _ := value.([]any); return v }
func count(value any) (int, error) {
	v, err := strconv.Atoi(text(value))
	if err != nil || v < 0 || v > 10000 {
		return 0, ErrSchema
	}
	return v, nil
}
func requireObject(value any) (map[string]any, error) {
	v := object(value)
	if v == nil {
		return nil, ErrSchema
	}
	return v, nil
}

func tencentData(result map[string]any) (any, error) {
	if text(result["status"]) != "0" {
		return nil, ErrSchema
	}
	return result["data"], nil
}
func huaweiData(result map[string]any) (any, error) {
	if text(result["status"]) != "SUCCESS" {
		return nil, ErrSchema
	}
	return result["data"], nil
}
func cmbData(result map[string]any) (map[string]any, error) {
	if text(result["returnCode"]) != "SUC0000" {
		return nil, ErrSchema
	}
	return requireObject(result["body"])
}

func joinedStrings(value any, field string) string {
	parts := []string{}
	for _, entry := range array(value) {
		v := text(entry)
		if v == "" && field != "" {
			v = text(object(entry)[field])
		}
		if v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, "/")
}
