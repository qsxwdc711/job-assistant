package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"jobassistant/backend/internal/catalog"
)

// Tencent reads the ordinary campus batch using its current child-project evidence.
func (r *Reader) Tencent(ctx context.Context) ([]catalog.Document, int, error) {
	headers := map[string]string{"Referer": r.TencentBase + "/post.html?query=p_1"}
	response, err := r.json(ctx, http.MethodGet, r.TencentBase+"/api/v1/position/getProjectMapping", nil, headers)
	if err != nil {
		return nil, 0, err
	}
	mapping, err := tencentData(response)
	if err != nil {
		return nil, 0, err
	}
	var campaign map[string]any
	parentDisplay := ""
	for _, parent := range array(mapping) {
		p := object(parent)
		if text(p["id"]) != "1" {
			continue
		}
		parentDisplay = text(p["recruitRangDesc"])
		for _, child := range array(p["subProjectList"]) {
			c := object(child)
			if text(c["mappingId"]) == "1" && text(c["recruitType"]) == "1" {
				campaign = c
			}
		}
	}
	if campaign == nil || text(campaign["recruitYear"]) == "" {
		return nil, 0, ErrSchema
	}
	rules := []catalog.GraduationRule{}
	condition := PlainText(text(campaign["recruitRangDesc"]))
	if from, through, ok := graduationRange(condition); ok {
		rules = append(rules, catalog.GraduationRule{Region: "all", Education: "unknown", From: from, Through: through, Evidence: condition})
	}
	batchKey := "ordinary:" + text(campaign["mappingId"]) + ":" + text(campaign["recruitYear"])
	items := []map[string]any{}
	seen := map[string]bool{}
	total := -1
	for page := 1; page <= 100; page++ {
		input := map[string]any{"projectIdList": []int{}, "projectMappingIdList": []int{1}, "keyword": "", "bgList": []int{}, "workCountryType": 1, "workCityList": []int{}, "recruitCityList": []int{}, "positionFidList": []int{}, "pageIndex": page, "pageSize": 50}
		response, err := r.json(ctx, http.MethodPost, r.TencentBase+"/api/v1/position/searchPosition", input, headers)
		if err != nil {
			return nil, 0, err
		}
		data, err := tencentData(response)
		if err != nil {
			return nil, 0, err
		}
		payload, err := requireObject(data)
		if err != nil {
			return nil, 0, err
		}
		n, err := count(payload["count"])
		if err != nil {
			return nil, 0, err
		}
		if total < 0 {
			total = n
		}
		if n != total {
			return nil, 0, ErrSchema
		}
		entries := array(payload["positionList"])
		if total == 0 && len(entries) == 0 {
			break
		}
		if len(entries) == 0 {
			return nil, 0, ErrSchema
		}
		for _, entry := range entries {
			item := object(entry)
			id := text(item["postId"])
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
		if text(item["positionFamily"]) != "2" {
			r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: batchKey, ExternalID: text(item["postId"]), Reason: "当前官方岗位大类不在已接入的技术范围"})
			continue
		}
		id := text(item["postId"])
		response, err := r.json(ctx, http.MethodGet, r.TencentBase+"/api/v1/jobDetails/getJobDetailsByPostId?postId="+url.QueryEscape(id), nil, headers)
		if err != nil {
			return nil, total, err
		}
		data, err := tencentData(response)
		if err != nil {
			return nil, total, err
		}
		detail, err := requireObject(data)
		if err != nil {
			return nil, total, err
		}
		if text(detail["postId"]) != id || text(detail["title"]) == "" {
			return nil, total, ErrSchema
		}
		title := PlainText(text(detail["title"]))
		duties := PlainText(text(detail["desc"]))
		requirements := PlainText(text(detail["request"]))
		direction := Direction(title, duties)
		if direction == "unknown" {
			r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: batchKey, ExternalID: id, Reason: "岗位名称与职责未确认属于计算机方向，需人工复核"})
			continue
		}
		departments := []catalog.DepartmentGroup{}
		for _, bg := range array(detail["intentionBGDList"]) {
			for _, dept := range array(object(bg)["departmentList"]) {
				d := object(dept)
				departments = append(departments, catalog.DepartmentGroup{Code: text(d["id"]), Name: PlainText(text(d["name"])), Locations: Locations(joinedStrings(d["workCityList"], ""))})
			}
		}
		link := "https://join.qq.com/post_detail.html?postid=" + url.QueryEscape(id)
		documents = append(documents, catalog.Document{ExternalID: id, CompanyCode: "tencent", SourceCode: "tencent-campus", CampaignKey: batchKey, CampaignName: text(campaign["projectName"]), Title: title, RecruitmentType: "campus", Publisher: "具体招聘主体以官方申请页面为准", PublicStatus: "unknown", StatusEvidence: "当前官方公开列表在列，详情可读；详情状态未明确", WrittenTest: "unknown", SourceURL: link, ApplyURL: link, ApplyLevel: "job", LoginRequired: true, DeadlineTimezone: "unknown", GraduationRules: rules, StudentRequired: "unknown", MinimumEducation: "unknown", CampaignEvidence: fmt.Sprintf("当前子批次：%s\n父菜单显示（不作为门槛）：%s", condition, parentDisplay), Variants: []catalog.Variant{{Key: "main", Name: title, Direction: direction, Responsibilities: duties, Requirements: requirements, Preferred: PlainText(text(detail["graduateBonus"])), Major: ParseMajor(requirements), Locations: Locations(joinedStrings(detail["workCityList"], "")), Departments: departments}}})
	}
	return documents, total, nil
}
