package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	"jobassistant/backend/internal/catalog"
)

var feishuID = regexp.MustCompile(`^[0-9]{10,24}$`)
var feishuInitial = regexp.MustCompile(`(?is)<script\b[^>]*\bid\s*=\s*["']js-websiteInfo["'][^>]*>(.*?)</script\s*>`)

func feishuPage(body []byte, site feishuSite) (string, error) {
	matches := feishuInitial.FindAllSubmatch(body, -1)
	if len(matches) != 1 {
		return "", ErrSchema
	}
	info, err := mokaObject(matches[0][1])
	if err != nil {
		return "", err
	}
	website, tenant := object(info["website_info"]), object(info["tenant_info"])
	if !feishuID.MatchString(text(website["id"])) || text(website["path"]) != site.path || !strings.Contains(text(tenant["tenant_name"]), site.tenant) {
		return "", ErrSchema
	}
	return text(website["id"]), nil
}

func (r *Reader) feishuQuery(ctx context.Context, method, endpoint string, input any, headers map[string]string) (map[string]any, error) {
	body, err := r.read(ctx, method, endpoint, input, headers)
	if err != nil {
		return nil, err
	}
	value, err := mokaObject(body)
	if err != nil {
		return nil, err
	}
	if text(value["code"]) != "0" {
		message := strings.ToLower(text(value["message"]) + text(value["error"]))
		if text(value["code"]) == "401" || text(value["code"]) == "403" || text(value["code"]) == "429" || strings.Contains(message, "csrf") || strings.Contains(message, "captcha") || strings.Contains(message, "登录") || strings.Contains(message, "验证") {
			return nil, ErrRestricted
		}
		return nil, ErrSchema
	}
	return requireObject(value["data"])
}

// Feishu reads the site's normal anonymous public session, never applicant data.
// A reader owns its cookie jar for one bounded complete snapshot.
func (r *Reader) Feishu(ctx context.Context, source string) ([]catalog.Document, int, error) {
	site, ok := feishuConfig(source)
	if !ok {
		return nil, 0, ErrSchema
	}
	base := site.origin
	if r.FeishuBase != "" {
		base = r.FeishuBase
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, 0, err
	}
	client := *r.Client
	client.Jar = jar
	oldClient, oldSpacing := r.Client, r.readSpacing
	r.Client, r.readSpacing = &client, r.feishuReadSpacing
	defer func() { r.Client, r.readSpacing = oldClient, oldSpacing }()
	page, err := r.read(ctx, http.MethodGet, base+"/"+site.path+"/position/list", nil, nil)
	if err != nil {
		return nil, 0, err
	}
	websiteID, err := feishuPage(page, site)
	if err != nil {
		return nil, 0, fmt.Errorf("feishu public tenant identity: %w", err)
	}
	headers := map[string]string{"Origin": base, "Referer": base + "/" + site.path + "/position/list", "website-path": site.path}
	session, err := r.feishuQuery(ctx, http.MethodPost, base+"/api/v1/csrf/token", map[string]any{"portal_entrance": 1}, headers)
	if err != nil {
		return nil, 0, err
	}
	token := text(session["token"])
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, "\r\n") {
		return nil, 0, ErrSchema
	}
	headers["X-CSRF-Token"] = token
	query := url.Values{"portal_type": {fmt.Sprint(site.portal)}, "portal_entrance": {"1"}}
	readList := func() ([]map[string]any, int, error) {
		entries := []map[string]any{}
		seen := map[string]bool{}
		total := -1
		for offset := 0; total < 0 || offset < total; offset += 10 {
			input := map[string]any{"keyword": "", "limit": 10, "offset": offset, "portal_type": site.portal, "portal_entrance": 1}
			for _, name := range []string{"job_category_id_list", "tag_id_list", "location_code_list", "subject_id_list", "recruitment_id_list", "job_function_id_list", "storefront_id_list"} {
				input[name] = []string{}
			}
			data, err := r.feishuQuery(ctx, http.MethodPost, base+"/api/v1/search/job/posts?"+query.Encode(), input, headers)
			if err != nil {
				return nil, max(total, 0), err
			}
			n, err := count(data["count"])
			if err != nil || n > 2000 || (total >= 0 && n != total) {
				return nil, max(total, 0), fmt.Errorf("feishu list count at offset %d: %w", offset, ErrSchema)
			}
			total = n
			jobs, valid := data["job_post_list"].([]any)
			if !valid || len(jobs) != min(10, total-offset) {
				return nil, total, fmt.Errorf("feishu short or malformed page at offset %d: %w", offset, ErrSchema)
			}
			for _, raw := range jobs {
				job := object(raw)
				id := text(job["id"])
				if !feishuID.MatchString(id) || seen[id] || PlainText(text(job["title"])) == "" || text(object(job["recruit_type"])["id"]) == "" {
					return nil, total, fmt.Errorf("feishu list identity at offset %d: %w", offset, ErrSchema)
				}
				seen[id] = true
				entries = append(entries, job)
			}
		}
		return entries, total, nil
	}
	entries, total, err := readList()
	if err != nil {
		return nil, total, err
	}
	documents := []catalog.Document{}
	for _, entry := range entries {
		id := text(entry["id"])
		exclude := func(reason string) {
			r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: feishuCampaign(entry, websiteID), ExternalID: id, Reason: reason})
		}
		if feishuDirection(entry) == "unknown" {
			exclude("未核实为计算机技术职责，暂不发布")
			continue
		}
		data, err := r.feishuQuery(ctx, http.MethodGet, base+"/api/v1/job/posts/"+id+"?portal_type="+fmt.Sprint(site.portal)+"&with_recommend=false", nil, headers)
		if err != nil {
			return nil, total, err
		}
		detail, err := requireObject(data["job_post_detail"])
		if err != nil || text(detail["id"]) != id || text(detail["title"]) != text(entry["title"]) || text(detail["channel_online_status"]) != "1" || text(object(detail["recruit_type"])["id"]) != text(object(entry["recruit_type"])["id"]) {
			return nil, total, fmt.Errorf("feishu detail %s identity or open status: %w", id, ErrSchema)
		}
		for _, key := range []string{"description", "requirement"} {
			if value, present := entry[key]; present && value != nil && PlainText(text(value)) != PlainText(text(detail[key])) {
				return nil, total, fmt.Errorf("feishu detail %s changed %s: %w", id, key, ErrSchema)
			}
		}
		if text(object(detail["job_subject"])["id"]) != text(object(entry["job_subject"])["id"]) {
			return nil, total, fmt.Errorf("feishu detail %s changed subject: %w", id, ErrSchema)
		}
		doc, reason, err := feishuDocument(site, websiteID, detail)
		if err != nil {
			return nil, total, fmt.Errorf("feishu document %s: %w", id, err)
		}
		if reason != "" {
			exclude(reason)
			continue
		}
		documents = append(documents, doc)
	}
	verified, verifiedTotal, err := readList()
	if err != nil {
		return nil, total, err
	}
	if verifiedTotal != total || len(verified) != len(entries) {
		return nil, total, fmt.Errorf("feishu changed final list count: %w", ErrSchema)
	}
	byID := make(map[string]map[string]string, len(verified))
	for _, job := range verified {
		byID[text(job["id"])] = feishuListContent(job)
	}
	for _, job := range entries {
		if !reflect.DeepEqual(feishuListContent(job), byID[text(job["id"])]) {
			return nil, total, fmt.Errorf("feishu changed final list content: %w", ErrSchema)
		}
	}
	return documents, total, nil
}

// feishuListContent excludes publication timestamps and sort scores. A second
// complete scan must retain the same identities and disclosed business content.
func feishuListContent(job map[string]any) map[string]string {
	return map[string]string{"id": text(job["id"]), "title": text(job["title"]), "type": text(object(job["recruit_type"])["id"]), "parent_type": text(object(object(job["recruit_type"])["parent"])["id"]), "subject": text(object(job["job_subject"])["id"]), "description": PlainText(text(job["description"])), "requirement": PlainText(text(job["requirement"]))}
}
