package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"jobassistant/backend/internal/catalog"
)

func (r *Reader) huaweiRequest(ctx context.Context, endpoint string, input any) (any, error) {
	headers := map[string]string{"X-HW-ID": "app_000000035886", "x-jalor-tenantAlias": "hcm", "x-language": "zh_CN", "x-Referer": r.HuaweiBase + "/cn", "x-alb-gray": "prod", "Origin": r.HuaweiBase, "Referer": r.HuaweiBase + "/cn/campus-recruitment-job-list?recruitmentType=FRESH_GRADUATE"}
	response, err := r.json(ctx, http.MethodPost, r.HuaweiAPI+"/recruitmentPosition/pub/"+endpoint+"?X-HW-ID=app_000000035886", input, headers)
	if err != nil {
		return nil, err
	}
	return huaweiData(response)
}

// Huawei reads job intentions and preserves their department/place relationships.
func (r *Reader) Huawei(ctx context.Context) ([]catalog.Document, int, error) {
	body, err := r.read(ctx, http.MethodGet, r.HuaweiBase+"/cn/campus-recruitment", nil, nil)
	if err != nil {
		return nil, 0, err
	}
	year, rules, campaignEvidence, examEvidence, err := huaweiCampaign(body)
	if err != nil {
		return nil, 0, err
	}
	items := []map[string]any{}
	seen := map[string]bool{}
	total := -1
	for page := 1; page <= 100; page++ {
		data, err := r.huaweiRequest(ctx, "getJobPage", map[string]any{"curPage": page, "pageSize": 100, "jobType": "CR", "recruitmentType": []string{"FRESH_GRADUATE"}})
		if err != nil {
			return nil, 0, err
		}
		payload, err := requireObject(data)
		if err != nil {
			return nil, 0, err
		}
		pageVO, err := requireObject(payload["pageVO"])
		if err != nil {
			return nil, 0, err
		}
		n, err := count(pageVO["totalRows"])
		if err != nil {
			return nil, 0, err
		}
		if total < 0 {
			total = n
		}
		if total != n {
			return nil, 0, ErrSchema
		}
		entries := array(payload["result"])
		if total == 0 && len(entries) == 0 {
			break
		}
		if len(entries) == 0 {
			return nil, 0, ErrSchema
		}
		for _, entry := range entries {
			item := object(entry)
			id := text(item["advertisementId"])
			if id == "" || seen[id] {
				return nil, 0, ErrSchema
			}
			seen[id] = true
			items = append(items, item)
		}
		if len(items) >= total {
			break
		}
	}
	if len(items) != total {
		return nil, 0, ErrSchema
	}
	documents := []catalog.Document{}
	for _, item := range items {
		if text(item["categoryName"]) != "研发类" {
			r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: "fresh:" + year, ExternalID: text(item["advertisementId"]), Reason: "当前官方类别不在已验证的研发计算机范围"})
			continue
		}
		id := text(item["advertisementId"])
		data, err := r.huaweiRequest(ctx, "getRecruitmentPositionDetail", map[string]any{"advertisementId": item["advertisementId"]})
		if err != nil {
			return nil, total, err
		}
		detail, err := requireObject(data)
		if err != nil {
			return nil, total, err
		}
		if text(detail["advertisementId"]) != id || text(detail["jobId"]) == "" {
			return nil, total, ErrSchema
		}
		data, err = r.huaweiRequest(ctx, "getPositionIntentionList", map[string]any{"jobId": detail["jobId"]})
		if err != nil {
			return nil, total, err
		}
		intentions := array(data)
		if len(intentions) == 0 {
			return nil, total, ErrSchema
		}
		title := PlainText(text(detail["jobname"]))
		if title == "" {
			return nil, total, ErrSchema
		}
		variants := []catalog.Variant{}
		variantIDs := map[string]bool{}
		for _, entry := range intentions {
			intent := object(entry)
			key := text(intent["positionIntentionId"])
			if key == "" || variantIDs[key] {
				return nil, total, ErrSchema
			}
			variantIDs[key] = true
			name := PlainText(text(intent["positionIntention"]))
			duties := PlainText(text(intent["jobResponsibilities"]))
			requirements := PlainText(text(intent["jobDemand"]))
			if name == "" || duties == "" || requirements == "" {
				return nil, total, ErrSchema
			}
			direction := Direction(name, duties)
			if direction == "unknown" {
				direction = Direction(title, "")
			}
			if direction == "unknown" {
				continue
			}
			departments := []catalog.DepartmentGroup{}
			for _, group := range array(intent["deptAndPlaceList"]) {
				g := object(group)
				departments = append(departments, catalog.DepartmentGroup{Code: text(g["deptCode"]), Name: PlainText(text(g["deptName"])), Locations: Locations(text(g["jobPlaceName"]))})
			}
			variants = append(variants, catalog.Variant{Key: key, Name: name, Direction: direction, Responsibilities: duties, Requirements: requirements, Major: ParseMajor(requirements), Locations: Locations(text(intent["jobPlaceName"])), Departments: departments})
		}
		if len(variants) == 0 {
			r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: "fresh:" + year, ExternalID: id, Reason: "当前岗位意向未确认属于计算机方向，需人工复核"})
			continue
		}
		link := "https://career.huawei.com/cn/job-details?advertisementId=" + url.QueryEscape(id)
		written := "unknown"
		if examEvidence != "" {
			written = "yes"
		}
		documents = append(documents, catalog.Document{ExternalID: id, CompanyCode: "huawei", SourceCode: "huawei-campus", CampaignKey: "fresh:" + year, CampaignName: year + "届应届生招聘", Title: title, RecruitmentType: "campus", Publisher: "法律招聘主体未披露；具体部门和地点见各岗位方向", PublicStatus: "open", StatusEvidence: "当前官方在招职位列表、广告详情和岗位意向均可读", WrittenTest: written, WrittenTestEvidence: examEvidence, SourceURL: link, ApplyURL: link, ApplyLevel: "job", LoginRequired: true, DeadlineTimezone: "unknown", GraduationRules: rules, StudentRequired: "unknown", MinimumEducation: "unknown", CampaignEvidence: fmt.Sprintf("%s\n依据：https://career.huawei.com/cn/campus-recruitment", campaignEvidence), Variants: variants})
	}
	return documents, total, nil
}
