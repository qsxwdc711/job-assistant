package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"jobassistant/backend/internal/catalog"
)

const zjditSource = "zjdit-careers"

func zjditLabels(value any) ([]string, error) {
	values, ok := value.([]any)
	if !ok || len(values) == 0 {
		return nil, ErrSchema
	}
	labels := make([]string, 0, len(values))
	for _, raw := range values {
		label := text(raw)
		if label != "校招" && label != "社招" || slices.Contains(labels, label) {
			return nil, ErrSchema
		}
		labels = append(labels, label)
	}
	slices.Sort(labels)
	return labels, nil
}

// ZJDIT reads the verified employer's complete public technical-job listing.
// It creates no applicant account and owns only an ephemeral anonymous cookie jar.
func (r *Reader) ZJDIT(ctx context.Context) ([]catalog.Document, int, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, 0, err
	}
	client := *r.Client
	client.Jar = jar
	oldClient, oldSpacing := r.Client, r.readSpacing
	r.Client, r.readSpacing = &client, 500*time.Millisecond
	defer func() { r.Client, r.readSpacing = oldClient, oldSpacing }()
	page, err := r.read(ctx, http.MethodGet, r.ZJDITBase+"/position/index.html?class_id=1", nil, nil)
	if err != nil {
		return nil, 0, err
	}
	if !strings.Contains(string(page), "四川中久数创科技有限公司招聘") {
		return nil, 0, ErrSchema
	}
	headers := map[string]string{"Origin": r.ZJDITBase, "Referer": r.ZJDITBase + "/position/index.html?class_id=1"}
	total := -1
	var entries []map[string]any
	seen := map[string]bool{}
	for page := 1; total < 0 || (page-1)*15 < total; page++ {
		input := url.Values{"class_id": {"1"}, "page": {strconv.Itoa(page)}, "limit": {"15"}, "location": {""}, "search": {""}}
		value, err := r.json(ctx, http.MethodPost, r.ZJDITBase+"/Position/loading_list_data.html", input, headers)
		if err != nil {
			return nil, max(total, 0), err
		}
		if code := text(value["code"]); code != "200" {
			if code == "401" || code == "403" || code == "429" {
				return nil, max(total, 0), ErrRestricted
			}
			return nil, max(total, 0), ErrSchema
		}
		n, err := count(value["count"])
		jobs, valid := value["data"].([]any)
		if err != nil || n > 1000 || (total >= 0 && n != total) || !valid || len(jobs) != min(15, n-(page-1)*15) {
			return nil, max(total, 0), ErrSchema
		}
		total = n
		for _, raw := range jobs {
			entry := object(raw)
			id, err := strconv.Atoi(text(entry["id"]))
			if err != nil || id <= 0 || strconv.Itoa(id) != text(entry["id"]) || seen[text(entry["id"])] || text(entry["class_id"]) != "1" || text(entry["listing_delisting"]) != "1" || text(entry["is_del"]) != "0" || PlainText(text(entry["name"])) == "" {
				return nil, total, ErrSchema
			}
			if _, err := zjditLabels(entry["labels"]); err != nil {
				return nil, total, err
			}
			seen[text(entry["id"])] = true
			entries = append(entries, entry)
		}
	}
	documents := []catalog.Document{}
	for _, entry := range entries {
		id := text(entry["id"])
		exclude := func(reason string) {
			r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: "zjdit:technical", ExternalID: id, Reason: reason})
		}
		if zjditDirection(PlainText(text(entry["name"]))) == "unknown" {
			exclude("当前职责方向未确认属于计算机技术岗位，保留待人工核验")
			continue
		}
		body, err := r.read(ctx, http.MethodGet, r.ZJDITBase+"/Position/details.html?id="+id, nil, headers)
		if err != nil {
			return nil, total, fmt.Errorf("zjdit detail %s: %w", id, err)
		}
		if err := zjditVerifyDetail(body, entry); err != nil {
			return nil, total, fmt.Errorf("zjdit detail %s: %w", id, err)
		}
		doc, reason, err := zjditDocument(entry)
		if err != nil {
			return nil, total, err
		}
		if reason != "" {
			exclude(reason)
			continue
		}
		documents = append(documents, doc)
	}
	return documents, total, nil
}
