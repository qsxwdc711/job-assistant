package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	"jobassistant/backend/internal/catalog"
)

var (
	beisenGlobal    = regexp.MustCompile(`\bvar\s+BSGlobal\s*=`)
	beisenUUID      = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
	beisenNumericID = regexp.MustCompile(`^[1-9][0-9]{0,15}$`)
)

func beisenHome(body []byte, site beisenSite) error {
	markers := beisenGlobal.FindAllIndex(body, -1)
	if len(markers) != 1 {
		return ErrSchema
	}
	decoder := json.NewDecoder(strings.NewReader(string(body[markers[0][1]:])))
	decoder.UseNumber()
	var config map[string]any
	if err := decoder.Decode(&config); err != nil {
		return ErrSchema
	}
	tenant := object(config["tenantInfo"])
	if text(tenant["Id"]) != fmt.Sprint(site.tenantID) || text(tenant["Status"]) != "1" || !strings.Contains(text(tenant["Alias"]), site.companyName) {
		return ErrSchema
	}
	return nil
}

func beisenResponse(value map[string]any) error {
	switch text(value["Code"]) {
	case "200":
		return nil
	case "401", "403", "429":
		return ErrRestricted
	default:
		return ErrSchema
	}
}

func beisenIdentity(job map[string]any) bool {
	title, validTitle := job["JobAdName"].(string)
	return beisenUUID.MatchString(text(job["Id"])) && beisenNumericID.MatchString(text(job["JobAdId"])) && validTitle && strings.TrimSpace(title) != "" && text(job["CategoryId"]) == "2" && (text(job["Status"]) == "1" || text(job["Status"]) == "2")
}

// beisenContent ignores session flags, sort scores and transport timestamps.
func beisenContent(job map[string]any) map[string]any {
	content := map[string]any{}
	for _, key := range []string{"Id", "JobAdId", "JobAdName", "CategoryId", "Status", "Kind", "LocNames", "Duty", "Require"} {
		content[key] = job[key]
	}
	return content
}

// Beisen reads a complete, stable public campus snapshot for a verified tenant.
// Each call owns an anonymous session and never reads applicant or login APIs.
func (r *Reader) Beisen(ctx context.Context, source string) ([]catalog.Document, int, error) {
	site, ok := beisenConfig(source)
	if !ok {
		return nil, 0, ErrSchema
	}
	base := site.origin
	if r.BeisenBase != "" {
		base = strings.TrimRight(r.BeisenBase, "/")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, 0, err
	}
	client := *r.Client
	client.Jar = jar
	oldClient, oldSpacing := r.Client, r.readSpacing
	r.Client, r.readSpacing = &client, max(oldSpacing, 500*time.Millisecond)
	defer func() { r.Client, r.readSpacing = oldClient, oldSpacing }()
	home, err := r.read(ctx, http.MethodGet, base+"/campus/jobs", nil, nil)
	if err != nil {
		return nil, 0, err
	}
	if err := beisenHome(home, site); err != nil {
		return nil, 0, fmt.Errorf("beisen public tenant: %w", err)
	}
	headers := map[string]string{"Origin": base, "Referer": base + "/campus/jobs"}
	readList := func() ([]map[string]any, int, error) {
		jobs := []map[string]any{}
		seen, total := map[string]bool{}, -1
		for page := 0; total < 0 || len(jobs) < total; page++ {
			body := map[string]any{"PageIndex": page, "PageSize": 20, "Category": []string{"2"}, "KeyWords": "", "SpecialType": 0, "PortalId": "", "DisplayFields": []string{"Kind", "LocId", "PostDate", "Salary", "WorkWeChatQrCode"}}
			value, err := r.json(ctx, http.MethodPost, base+"/api/Jobad/GetJobAdPageList", body, headers)
			if err != nil {
				return nil, max(total, 0), err
			}
			if err := beisenResponse(value); err != nil {
				return nil, max(total, 0), err
			}
			n, err := count(value["Count"])
			entries, valid := value["Data"].([]any)
			if err != nil || n <= 0 || n > 1000 || !valid || (total >= 0 && n != total) || len(entries) != min(20, n-len(jobs)) {
				return nil, max(total, 0), ErrSchema
			}
			total = n
			for _, raw := range entries {
				job := object(raw)
				id := text(job["Id"])
				if !beisenIdentity(job) || seen[id] {
					return nil, total, ErrSchema
				}
				seen[id] = true
				jobs = append(jobs, job)
			}
		}
		return jobs, total, nil
	}
	jobs, total, err := readList()
	if err != nil {
		return nil, total, err
	}
	documents := []catalog.Document{}
	for _, job := range jobs {
		id := text(job["Id"])
		exclude := func(reason string) {
			r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: fmt.Sprintf("beisen:%d:2", site.tenantID), ExternalID: id, Reason: reason})
		}
		if text(job["Status"]) != "1" {
			exclude("官方完整列表未处于开放状态，保留待复核")
			continue
		}
		if beisenDirection(job) == "unknown" {
			exclude("岗位职责未确认属于计算机技术方向，保留待复核")
			continue
		}
		fields, err := json.Marshal([]string{"jobAdName", "Duty", "Require", "Category", "Kind", "LocId", "DetailAddress", "Org", "HeadCount", "Station", "EndTime", "PostDate", "Salary", "YearsOfWorking", "Degree", "ClassificationOne", "ClassificationTwo", "Classification3"})
		if err != nil {
			return nil, total, err
		}
		query := url.Values{"jobAdId": {id}, "category": {"2"}, "displayFields": {string(fields)}}
		value, err := r.json(ctx, http.MethodGet, base+"/api/JobAd/GetJobAdInfo?"+query.Encode(), nil, headers)
		if err != nil {
			return nil, total, err
		}
		if err := beisenResponse(value); err != nil {
			return nil, total, err
		}
		detail := object(value["Data"])
		if !beisenIdentity(detail) || !reflect.DeepEqual(beisenContent(job), beisenContent(detail)) || text(detail["Status"]) != "1" {
			return nil, total, fmt.Errorf("beisen detail identity or content: %w", ErrSchema)
		}
		doc, reason, err := beisenDocument(site, detail)
		if err != nil {
			return nil, total, err
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
	if verifiedTotal != total || len(verified) != len(jobs) {
		return nil, total, ErrSchema
	}
	byID := make(map[string]map[string]any, len(verified))
	for _, job := range verified {
		byID[text(job["Id"])] = job
	}
	for _, job := range jobs {
		if !reflect.DeepEqual(beisenContent(job), beisenContent(byID[text(job["Id"])])) {
			return nil, total, fmt.Errorf("beisen list changed during collection: %w", ErrSchema)
		}
	}
	return documents, total, nil
}
