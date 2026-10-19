package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"jobassistant/backend/internal/catalog"
)

const chaitinSource = "chaitin-careers"
const chaitinAPIPath = "/plugins/career_site/api/default/jobs"
const chaitinPagePath = "/plugins/career_site/sites/default"

var chaitinID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var chaitinTrackingCode = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var chaitinJobFields = []string{"job_id", "title", "department", "job_category_name", "location", "work_type", "recruitment_type", "description", "tracking_code", "career_site_published_at", "updated_at"}

func chaitinValidateJob(job map[string]any) error {
	for _, key := range chaitinJobFields[:9] {
		value, ok := job[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return ErrSchema
		}
	}
	if !chaitinID.MatchString(text(job["job_id"])) || !chaitinTrackingCode.MatchString(text(job["tracking_code"])) {
		return ErrSchema
	}
	if kind := text(job["recruitment_type"]); kind != "campus" && kind != "social" {
		return ErrSchema
	}
	if work := text(job["work_type"]); work != "full_time" && work != "internship" {
		return ErrSchema
	}
	for _, key := range chaitinJobFields[9:] {
		n, err := strconv.ParseInt(text(job[key]), 10, 64)
		if err != nil || n <= 0 {
			return ErrSchema
		}
	}
	return nil
}

func chaitinSuccess(value map[string]any) bool {
	code, ok := value["code"].(json.Number)
	return ok && code.String() == "0"
}

// Chaitin reads the complete, anonymous public site linked by the employer.
// Candidate documents are returned only after every required detail is verified.
func (r *Reader) Chaitin(ctx context.Context) ([]catalog.Document, int, error) {
	oldSpacing := r.readSpacing
	r.readSpacing = max(oldSpacing, r.chaitinReadSpacing)
	defer func() { r.readSpacing = oldSpacing }()
	entry, err := r.read(ctx, http.MethodGet, r.ChaitinBase+chaitinPagePath, nil, nil)
	if err != nil {
		return nil, 0, err
	}
	brand, err := chaitinBrandFromPage(entry)
	if err != nil || !strings.Contains(string(entry), "长亭科技招聘官网") {
		return nil, 0, ErrSchema
	}
	total := -1
	var jobs []map[string]any
	seen := map[string]bool{}
	for page := 1; total < 0 || (page-1)*10 < total; page++ {
		value, err := r.json(ctx, http.MethodGet, fmt.Sprintf("%s%s?page=%d&size=10", r.ChaitinBase, chaitinAPIPath, page), nil, nil)
		if err != nil {
			return nil, max(total, 0), err
		}
		data := object(value["data"])
		n, countErr := count(data["total_count"])
		items, valid := data["items"].([]any)
		next, nextValid := data["has_next_page"].(bool)
		if !chaitinSuccess(value) || countErr != nil || n > 1000 || total >= 0 && n != total || !valid || !nextValid || len(items) != min(10, n-(page-1)*10) || next != (page*10 < n) {
			return nil, max(total, 0), ErrSchema
		}
		total = n
		for _, raw := range items {
			job := object(raw)
			id := text(job["job_id"])
			if chaitinValidateJob(job) != nil || seen[id] {
				return nil, total, ErrSchema
			}
			seen[id] = true
			jobs = append(jobs, job)
		}
	}
	documents := []catalog.Document{}
	for _, job := range jobs {
		id := text(job["job_id"])
		exclude := func(reason string) {
			r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: "chaitin:default", ExternalID: id, Reason: reason})
		}
		if chaitinDirection(job) == "unknown" {
			exclude("官方类别或实际职责未确认属于计算机技术岗位，保留待人工核验")
			continue
		}
		value, err := r.json(ctx, http.MethodGet, r.ChaitinBase+chaitinAPIPath+"/"+id, nil, nil)
		if err != nil {
			return nil, total, fmt.Errorf("chaitin detail %s: %w", id, err)
		}
		detail := object(object(value["data"])["job"])
		if !chaitinSuccess(value) || chaitinValidateJob(detail) != nil {
			return nil, total, ErrSchema
		}
		for _, key := range chaitinJobFields {
			if !reflect.DeepEqual(job[key], detail[key]) {
				return nil, total, ErrSchema
			}
		}
		doc, reason, err := chaitinDocument(detail, brand)
		if err != nil {
			return nil, total, err
		}
		if reason != "" {
			exclude(reason)
			continue
		}
		body, err := r.read(ctx, http.MethodGet, r.ChaitinBase+chaitinPagePath+"/jobs/"+id, nil, nil)
		if err != nil {
			return nil, total, fmt.Errorf("chaitin job page %s: %w", id, err)
		}
		if err = chaitinVerifyPage(body, id, brand); err != nil {
			return nil, total, err
		}
		documents = append(documents, doc)
	}
	return documents, total, nil
}
