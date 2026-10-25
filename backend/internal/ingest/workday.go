package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"jobassistant/backend/internal/catalog"
)

const workdayPageSize = 20

var (
	workdayReqID = regexp.MustCompile(`^JR[0-9]{6,12}$`)
	workdayPath  = regexp.MustCompile(`^/job/[A-Za-z0-9_-]+/[A-Za-z0-9_-]+_(JR[0-9]{6,12})(?:-[0-9]+)?$`)
)

type workdayEntry struct {
	id, title, path string
	kind            workdayType
}

// Workday reads complete native China/type-filtered public CXS snapshots. No
// applicant session is used; a failure in any page or detail rejects the result.
func (r *Reader) Workday(ctx context.Context, source string) ([]catalog.Document, int, error) {
	site, ok := workdayConfig(source)
	if !ok {
		return nil, 0, ErrSchema
	}
	base := strings.TrimRight(r.WorkdayBase, "/")
	if base == "" {
		base = site.host
	}
	api := base + "/wday/cxs/" + site.tenant + "/" + site.site
	seen := map[string]bool{}
	var entries []workdayEntry
	fetched := 0
	for _, kind := range site.types {
		query := func(offset int) (map[string]any, error) {
			return r.json(ctx, http.MethodPost, api+"/jobs", map[string]any{
				"appliedFacets": map[string]any{"locationHierarchy1": []string{site.china}, "workerSubType": []string{kind.id}},
				"limit":         workdayPageSize, "offset": offset, "searchText": "",
			}, nil)
		}
		total := -1
		var firstPage []workdayEntry
		for offset := 0; total < 0 || offset < total; offset += workdayPageSize {
			result, err := query(offset)
			if err != nil {
				return nil, fetched, fmt.Errorf("workday %s list: %w", kind.name, err)
			}
			jobs, n, err := workdayPage(result, site, kind, total, offset)
			if err != nil {
				return nil, fetched, fmt.Errorf("workday %s completeness: %w", kind.name, ErrSchema)
			}
			total = n
			for _, raw := range jobs {
				job := object(raw)
				ids, idsOK := job["bulletFields"].([]any)
				id, title, externalPath := "", text(job["title"]), text(job["externalPath"])
				if idsOK && len(ids) == 1 {
					id = text(ids[0])
				}
				match := workdayPath.FindStringSubmatch(externalPath)
				if !workdayReqID.MatchString(id) || len(match) != 2 || match[1] != id || PlainText(title) == "" || seen[id] {
					return nil, fetched, fmt.Errorf("workday list identity: %w", ErrSchema)
				}
				seen[id] = true
				entry := workdayEntry{id: id, title: title, path: externalPath, kind: kind}
				entries = append(entries, entry)
				if offset == 0 {
					firstPage = append(firstPage, entry)
				}
				fetched++
			}
		}
		if total > workdayPageSize {
			result, err := query(0)
			if err != nil {
				return nil, fetched, fmt.Errorf("workday %s pagination recheck: %w", kind.name, err)
			}
			jobs, _, err := workdayPage(result, site, kind, total, 0)
			if err != nil {
				return nil, fetched, fmt.Errorf("workday %s changed pagination metadata: %w", kind.name, ErrSchema)
			}
			for i, raw := range jobs {
				job := object(raw)
				ids, ok := job["bulletFields"].([]any)
				if !ok || len(ids) != 1 || text(ids[0]) != firstPage[i].id || text(job["title"]) != firstPage[i].title || text(job["externalPath"]) != firstPage[i].path {
					return nil, fetched, fmt.Errorf("workday %s changed pagination identity: %w", kind.name, ErrSchema)
				}
			}
		}
	}
	var documents []catalog.Document
	for _, entry := range entries {
		detail, err := r.json(ctx, http.MethodGet, api+entry.path, nil, nil)
		if err != nil {
			return nil, fetched, fmt.Errorf("workday detail %s: %w", entry.id, err)
		}
		info := object(detail["jobPostingInfo"])
		if text(info["jobReqId"]) != entry.id || text(info["title"]) != entry.title || text(info["jobPostingSiteId"]) != site.site || text(info["jobPostingId"]) != path.Base(entry.path) || info["posted"] != true || info["canApply"] != true || !workdayApplyURL(site, text(info["externalUrl"]), entry.path) {
			return nil, fetched, fmt.Errorf("workday detail %s identity, posting or apply state: %w", entry.id, ErrSchema)
		}
		doc, reason, err := workdayDocument(site, entry.kind, detail)
		if err != nil {
			return nil, fetched, fmt.Errorf("workday document %s: %w", entry.id, err)
		}
		if reason != "" {
			r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: "workday:" + site.site, ExternalID: entry.id, Reason: reason})
			continue
		}
		documents = append(documents, doc)
	}
	return documents, fetched, nil
}

// workdayPage retains first-page metadata when subsequent CXS pages explicitly
// return total=0 and facets=[]. The first page is rechecked after pagination.
func workdayPage(result map[string]any, site workdaySite, kind workdayType, total, offset int) ([]any, int, error) {
	n, err := count(result["total"])
	jobs, jobsOK := result["jobPostings"].([]any)
	facets, facetsOK := result["facets"].([]any)
	if err != nil || n > 1000 || !jobsOK || !facetsOK {
		return nil, 0, ErrSchema
	}
	if offset == 0 {
		if total >= 0 && n != total {
			return nil, 0, ErrSchema
		}
		total = n
	} else if total < 0 || (n != 0 && n != total) {
		return nil, 0, ErrSchema
	}
	if total < offset || len(jobs) != min(workdayPageSize, total-offset) {
		return nil, 0, ErrSchema
	}
	if total > 0 && (offset == 0 || len(facets) > 0) {
		id, typeCount, valid := workdayFacet(facets, "workerSubType", kind.name)
		china, chinaCount, chinaOK := workdayFacet(facets, "locationHierarchy1", "China")
		if !valid || !chinaOK || id != kind.id || china != site.china || typeCount != total || chinaCount != total {
			return nil, 0, ErrSchema
		}
	}
	return jobs, total, nil
}

func workdayApplyURL(site workdaySite, rawURL, externalPath string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.User != nil || u.Scheme != "https" || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	root, err := url.Parse(site.host)
	return err == nil && u.Host == root.Host && u.EscapedPath() == "/"+site.site+externalPath
}

// workdayFacet follows nested public facets and requires one exact descriptor.
func workdayFacet(entries []any, parameter, descriptor string) (string, int, bool) {
	var id string
	var n int
	found := 0
	var visit func([]any)
	visit = func(values []any) {
		for _, raw := range values {
			entry := object(raw)
			if text(entry["facetParameter"]) == parameter {
				for _, item := range array(entry["values"]) {
					value := object(item)
					if text(value["descriptor"]) == descriptor {
						v, err := count(value["count"])
						if err == nil {
							id, n, found = text(value["id"]), v, found+1
						}
					}
				}
			}
			visit(array(entry["values"]))
		}
	}
	visit(entries)
	return id, n, found == 1 && id != ""
}
