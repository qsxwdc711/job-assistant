package ingest

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"time"

	"jobassistant/backend/internal/catalog"
)

type mokaSite struct {
	source, company, org, site, mode, path, endpoint string
}

func mokaConfig(source string) (mokaSite, bool) {
	switch source {
	case "nsfocus-campus":
		return mokaSite{source: source, company: "nsfocus", org: "nsfocus", site: "29118", mode: "campus", path: "/campus_apply/nsfocus/29118", endpoint: "/api/outer/ats-apply/website/jobs/v2"}, true
	case "shokz-campus":
		return mokaSite{source: source, company: "shokz", org: "aftershokzhr", site: "36940", mode: "campus", path: "/campus-recruitment/aftershokzhr/36940", endpoint: "/api/outer/ats-apply/website/jobs/v2"}, true
	case "bluepoch-careers":
		return mokaSite{source: source, company: "bluepoch", org: "blueinteractive", site: "38433", mode: "social", path: "/apply/blueinteractive/38433", endpoint: "/api/outer/ats-apply/website/jobs"}, true
	}
	return mokaSite{}, false
}

// SupportsSource identifies implemented adapters, never arbitrary user-supplied URLs.
func SupportsSource(source string) bool {
	switch source {
	case "tencent-campus", "tencent-internships", "huawei-campus", "huawei-internships", "cmb-campus":
		return true
	}
	_, ok := mokaConfig(source)
	return ok
}

var mokaInputs = regexp.MustCompile(`(?is)<input\b[^>]*>`)
var mokaAttributes = regexp.MustCompile(`(?is)([a-zA-Z][a-zA-Z0-9_-]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
var mokaID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func mokaObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, ErrSchema
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, ErrSchema
	}
	return value, nil
}

func mokaInit(body []byte, site mokaSite) (string, error) {
	var found map[string]any
	for _, tag := range mokaInputs.FindAll(body, -1) {
		attributes := map[string]string{}
		for _, entry := range mokaAttributes.FindAllSubmatch(tag, -1) {
			attributes[strings.ToLower(string(entry[1]))] = html.UnescapeString(string(entry[2]) + string(entry[3]))
		}
		if attributes["id"] != "init-data" {
			continue
		}
		if found != nil {
			return "", ErrSchema
		}
		value, err := mokaObject([]byte(attributes["value"]))
		if err != nil {
			return "", err
		}
		found = value
	}
	if text(object(found["org"])["id"]) != site.org || text(found["siteId"]) != site.site || text(found["mode"]) != site.mode || len(text(found["aesIv"])) != aes.BlockSize {
		return "", ErrSchema
	}
	return text(found["aesIv"]), nil
}

// mokaDecode follows the public page's response envelope. These ephemeral values
// come from the public page/response and are never account credentials or stored.
func mokaDecode(body []byte, iv string) (map[string]any, error) {
	value, err := mokaObject(body)
	if err != nil {
		return nil, err
	}
	if key := text(value["necromancer"]); key != "" {
		if len(key) != aes.BlockSize || len(iv) != aes.BlockSize {
			return nil, ErrSchema
		}
		encrypted, err := base64.StdEncoding.DecodeString(text(value["data"]))
		if err != nil || len(encrypted) == 0 || len(encrypted)%aes.BlockSize != 0 {
			return nil, ErrSchema
		}
		block, err := aes.NewCipher([]byte(key))
		if err != nil {
			return nil, ErrSchema
		}
		plain := make([]byte, len(encrypted))
		cipher.NewCBCDecrypter(block, []byte(iv)).CryptBlocks(plain, encrypted)
		padding := int(plain[len(plain)-1])
		if padding == 0 || padding > aes.BlockSize || padding > len(plain) {
			return nil, ErrSchema
		}
		for _, v := range plain[len(plain)-padding:] {
			if int(v) != padding {
				return nil, ErrSchema
			}
		}
		value, err = mokaObject(plain[:len(plain)-padding])
		if err != nil {
			return nil, err
		}
	}
	if text(value["code"]) != "0" || value["success"] != true {
		message := text(value["msg"])
		if text(value["code"]) == "403" || text(value["code"]) == "401" || text(value["code"]) == "429" || strings.Contains(message, "登录") || strings.Contains(message, "验证码") {
			return nil, ErrRestricted
		}
		return nil, ErrSchema
	}
	return requireObject(value["data"])
}

// Moka reads a complete verified tenant snapshot without any applicant account.
// One reader owns its anonymous cookie jar and is not used concurrently.
func (r *Reader) Moka(ctx context.Context, source string) ([]catalog.Document, int, error) {
	site, ok := mokaConfig(source)
	if !ok {
		return nil, 0, ErrSchema
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, 0, err
	}
	client := *r.Client
	client.Jar = jar
	// This host/proxy closes hybrid-ML-KEM ClientHello connections. Use standard
	// X25519/NIST exchanges for this reader only; certificate verification and
	// TLS version defaults remain intact, as do the other adapters' transports.
	baseTransport, cloneTransport := r.Client.Transport.(*http.Transport)
	if r.Client.Transport == nil {
		baseTransport, cloneTransport = http.DefaultTransport.(*http.Transport), true
	}
	if cloneTransport {
		transport := baseTransport.Clone()
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		} else {
			transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		}
		if len(transport.TLSClientConfig.CurvePreferences) == 0 {
			transport.TLSClientConfig.CurvePreferences = []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384, tls.CurveP521}
		}
		client.Transport = transport
	}
	oldClient, oldSpacing := r.Client, r.readSpacing
	r.Client, r.readSpacing = &client, 500*time.Millisecond
	defer func() {
		if cloneTransport {
			client.CloseIdleConnections()
		}
		r.Client, r.readSpacing = oldClient, oldSpacing
	}()
	page, err := r.read(ctx, http.MethodGet, r.MokaBase+site.path, nil, nil)
	if err != nil {
		return nil, 0, err
	}
	iv, err := mokaInit(page, site)
	if err != nil {
		return nil, 0, err
	}
	headers := map[string]string{"Origin": r.MokaBase, "Referer": r.MokaBase + site.path}
	query := func(endpoint string, input any) (map[string]any, error) {
		body, err := r.read(ctx, http.MethodPost, r.MokaBase+endpoint, input, headers)
		if err != nil {
			return nil, err
		}
		return mokaDecode(body, iv)
	}
	var entries []map[string]any
	seen := map[string]bool{}
	total := -1
	for offset := 0; total < 0 || offset < total; offset += 15 {
		data, err := query(site.endpoint, map[string]any{"orgId": site.org, "siteId": site.site, "site": site.mode, "locale": "zh_CN", "needStat": true, "offset": offset, "limit": 15})
		if err != nil {
			return nil, max(total, 0), err
		}
		stats := object(data["jobStats"])
		n, err := count(stats["total"])
		if err != nil || n > 1000 || text(stats["orgId"]) != site.org || (total >= 0 && n != total) {
			return nil, max(total, 0), ErrSchema
		}
		total = n
		jobs, valid := data["jobs"].([]any)
		if !valid || len(jobs) != min(15, total-offset) {
			return nil, total, ErrSchema
		}
		for _, raw := range jobs {
			job := object(raw)
			id := text(job["id"])
			status := text(job["status"])
			if !mokaID.MatchString(id) || seen[id] || text(job["orgId"]) != site.org || text(job["title"]) == "" || (status != "open" && status != "pause") {
				return nil, total, ErrSchema
			}
			seen[id] = true
			entries = append(entries, job)
		}
	}
	documents := []catalog.Document{}
	for _, entry := range entries {
		id, title := text(entry["id"]), PlainText(text(entry["title"]))
		exclude := func(reason string) {
			r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: "moka:" + site.site, ExternalID: id, Reason: reason})
		}
		if text(entry["status"]) == "pause" {
			exclude("官方列表状态为 pause，暂不作为开放岗位发布；保留待复核，不推断永久关闭")
			continue
		}
		if mokaDirection(title) == "unknown" {
			exclude("当前岗位标题未确认属于计算机技术方向，需人工复核")
			continue
		}
		detail, err := query("/api/outer/ats-apply/website/job", map[string]any{"orgId": site.org, "siteId": site.site, "jobId": id, "locale": "zh_CN"})
		if err != nil {
			return nil, total, fmt.Errorf("moka detail %s: %w", id, err)
		}
		if text(detail["id"]) != id || text(detail["orgId"]) != site.org || text(detail["status"]) != "open" || text(detail["title"]) != text(entry["title"]) || text(detail["hireMode"]) != text(entry["hireMode"]) || text(detail["commitment"]) != text(entry["commitment"]) {
			return nil, total, fmt.Errorf("moka detail %s identity or status: %w", id, ErrSchema)
		}
		doc, reason, err := mokaDocument(site, detail)
		if err != nil {
			return nil, total, fmt.Errorf("moka document %s: %w", id, err)
		}
		if reason != "" {
			exclude(reason)
			continue
		}
		documents = append(documents, doc)
	}
	return documents, total, nil
}
