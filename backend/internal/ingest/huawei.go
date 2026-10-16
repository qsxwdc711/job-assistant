package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"jobassistant/backend/internal/catalog"
)

func (r *Reader) huaweiRequest(ctx context.Context, recruitmentType, endpoint string, input any) (any, error) {
	headers := map[string]string{"X-HW-ID": "app_000000035886", "x-jalor-tenantAlias": "hcm", "x-language": "zh_CN", "x-Referer": r.HuaweiBase + "/cn", "x-alb-gray": "prod", "Origin": r.HuaweiBase, "Referer": r.HuaweiBase + "/cn/campus-recruitment-job-list?recruitmentType=" + url.QueryEscape(recruitmentType)}
	response, err := r.json(ctx, http.MethodPost, r.HuaweiAPI+"/recruitmentPosition/pub/"+endpoint+"?X-HW-ID=app_000000035886", input, headers)
	if err != nil {
		return nil, err
	}
	return huaweiData(response)
}

// Huawei reads job intentions and preserves their department/place relationships.
func (r *Reader) Huawei(ctx context.Context) ([]catalog.Document, int, error) {
	return r.huawei(ctx, false)
}

// HuaweiInternships reads the INTERN programme without inheriting full-time conditions.
func (r *Reader) HuaweiInternships(ctx context.Context) ([]catalog.Document, int, error) {
	return r.huawei(ctx, true)
}

type huaweiProgramme struct {
	officialType, sourceCode, recruitmentType, campaignKey, campaignName string
	evidence, examEvidence, graduationPreference, studentRequired        string
	rules                                                                []catalog.GraduationRule
}

func (r *Reader) huawei(ctx context.Context, internship bool) ([]catalog.Document, int, error) {
	body, err := r.read(ctx, http.MethodGet, r.HuaweiBase+"/cn/campus-recruitment", nil, nil)
	if err != nil {
		return nil, 0, err
	}
	var programme huaweiProgramme
	if internship {
		programme, err = huaweiInternshipCampaign(body)
	} else {
		var year, evidence, exam string
		var rules []catalog.GraduationRule
		year, rules, evidence, exam, err = huaweiCampaign(body)
		programme = huaweiProgramme{officialType: "FRESH_GRADUATE", sourceCode: "huawei-campus", recruitmentType: "campus", campaignKey: "fresh:" + year, campaignName: year + "届应届生招聘", evidence: evidence, examEvidence: exam, studentRequired: "unknown", rules: rules}
	}
	if err != nil {
		return nil, 0, err
	}
	items := []map[string]any{}
	seen := map[string]bool{}
	total := -1
	for page := 1; page <= 100; page++ {
		data, err := r.huaweiRequest(ctx, programme.officialType, "getJobPage", map[string]any{"curPage": page, "pageSize": 100, "jobType": "CR", "recruitmentType": []string{programme.officialType}})
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
		list, exists := payload["result"]
		entries, valid := list.([]any)
		if !exists || (list != nil && !valid) {
			return nil, 0, ErrSchema
		}
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
			r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: programme.campaignKey, ExternalID: text(item["advertisementId"]), Reason: "当前官方类别不在已验证的研发计算机范围"})
			continue
		}
		id := text(item["advertisementId"])
		data, err := r.huaweiRequest(ctx, programme.officialType, "getRecruitmentPositionDetail", map[string]any{"advertisementId": item["advertisementId"]})
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
		data, err = r.huaweiRequest(ctx, programme.officialType, "getPositionIntentionList", map[string]any{"jobId": detail["jobId"]})
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
			r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: programme.campaignKey, ExternalID: id, Reason: "当前岗位意向未确认属于计算机方向，需人工复核"})
			continue
		}
		link := "https://career.huawei.com/cn/job-details?advertisementId=" + url.QueryEscape(id)
		written := "unknown"
		if programme.examEvidence != "" {
			written = "yes"
		}
		documents = append(documents, catalog.Document{ExternalID: id, CompanyCode: "huawei", SourceCode: programme.sourceCode, CampaignKey: programme.campaignKey, CampaignName: programme.campaignName, Title: title, RecruitmentType: programme.recruitmentType, Publisher: "法律招聘主体未披露；具体部门和地点见各岗位方向", PublicStatus: "open", StatusEvidence: "当前官方在招职位列表、广告详情和岗位意向均可读", WrittenTest: written, WrittenTestEvidence: programme.examEvidence, SourceURL: link, ApplyURL: link, ApplyLevel: "job", LoginRequired: true, DeadlineTimezone: "unknown", GraduationRules: programme.rules, GraduationPreference: programme.graduationPreference, StudentRequired: programme.studentRequired, MinimumEducation: "unknown", CampaignEvidence: fmt.Sprintf("%s\n依据：https://career.huawei.com/cn/campus-recruitment", programme.evidence), Variants: variants})
	}
	return documents, total, nil
}
