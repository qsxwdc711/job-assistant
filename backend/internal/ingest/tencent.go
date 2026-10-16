package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"jobassistant/backend/internal/catalog"
)

// Tencent reads the ordinary campus batch using its current child-project evidence.
func (r *Reader) Tencent(ctx context.Context) ([]catalog.Document, int, error) {
	return r.tencent(ctx, tencentProgramme{parentID: "1", mappingIDs: []string{"1"}, recruitType: "1", sourceCode: "tencent-campus", recruitmentType: "campus", keyPrefix: "ordinary", studentRequired: "unknown"})
}

// TencentInternships reads ordinary and daily internships, excluding special programmes.
func (r *Reader) TencentInternships(ctx context.Context) ([]catalog.Document, int, error) {
	return r.tencent(ctx, tencentProgramme{parentID: "2", mappingIDs: []string{"2", "104"}, recruitType: "2", sourceCode: "tencent-internships", recruitmentType: "internship", keyPrefix: "intern", studentRequired: "unknown"})
}

type tencentProgramme struct {
	parentID, recruitType, sourceCode, recruitmentType, keyPrefix, studentRequired string
	mappingIDs                                                                     []string
}

func (r *Reader) tencent(ctx context.Context, programme tencentProgramme) ([]catalog.Document, int, error) {
	headers := map[string]string{"Referer": r.TencentBase + "/post.html?query=p_1"}
	response, err := r.json(ctx, http.MethodGet, r.TencentBase+"/api/v1/position/getProjectMapping", nil, headers)
	if err != nil {
		return nil, 0, err
	}
	mapping, err := tencentData(response)
	if err != nil {
		return nil, 0, err
	}
	campaigns := map[string]map[string]any{}
	parentDisplay := ""
	for _, parent := range array(mapping) {
		p := object(parent)
		if text(p["id"]) != programme.parentID {
			continue
		}
		parentDisplay = text(p["recruitRangDesc"])
		for _, child := range array(p["subProjectList"]) {
			c := object(child)
			id := text(c["mappingId"])
			if campaigns[id] != nil {
				return nil, 0, ErrSchema
			}
			campaigns[id] = c
		}
	}
	documents := []catalog.Document{}
	seen := map[string]bool{}
	total := 0
	for _, id := range programme.mappingIDs {
		campaign := campaigns[id]
		if campaign == nil || text(campaign["recruitYear"]) == "" || text(campaign["recruitType"]) != programme.recruitType || text(campaign["status"]) != "1" || PlainText(text(campaign["recruitRangDesc"])) == "" {
			return nil, total, ErrSchema
		}
		docs, fetched, err := r.tencentBatch(ctx, programme, campaign, parentDisplay, headers, seen)
		total += fetched
		if err != nil {
			return nil, total, err
		}
		documents = append(documents, docs...)
	}
	return documents, total, nil
}

func (r *Reader) tencentBatch(ctx context.Context, programme tencentProgramme, campaign map[string]any, parentDisplay string, headers map[string]string, seen map[string]bool) ([]catalog.Document, int, error) {
	mappingID, err := strconv.Atoi(text(campaign["mappingId"]))
	if err != nil {
		return nil, 0, ErrSchema
	}
	rules := []catalog.GraduationRule{}
	condition := PlainText(text(campaign["recruitRangDesc"]))
	if programme.recruitmentType == "internship" && strings.Contains(condition, "面向全体在校生") {
		programme.studentRequired = "yes"
	}
	if from, through, ok := graduationRange(condition); ok {
		rules = append(rules, catalog.GraduationRule{Region: "all", Education: "unknown", From: from, Through: through, Evidence: condition})
	} else if strings.Contains(condition, "毕业时间") {
		return nil, 0, ErrSchema
	}
	batchKey := programme.keyPrefix + ":" + text(campaign["mappingId"]) + ":" + text(campaign["recruitYear"])
	items := []map[string]any{}
	total := -1
	for page := 1; page <= 100; page++ {
		input := map[string]any{"projectIdList": []int{}, "projectMappingIdList": []int{mappingID}, "keyword": "", "bgList": []int{}, "workCountryType": 1, "workCityList": []int{}, "recruitCityList": []int{}, "positionFidList": []int{}, "pageIndex": page, "pageSize": 50}
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
		list, exists := payload["positionList"]
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
		variants, err := tencentVariants(programme, mappingID, id, detail)
		if err != nil {
			return nil, total, err
		}
		if len(variants) == 0 {
			r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: batchKey, ExternalID: id, Reason: "岗位名称与职责未确认属于计算机方向，需人工复核"})
			continue
		}
		link := "https://join.qq.com/post_detail.html?postid=" + url.QueryEscape(id)
		documents = append(documents, catalog.Document{ExternalID: id, CompanyCode: "tencent", SourceCode: programme.sourceCode, CampaignKey: batchKey, CampaignName: text(campaign["projectName"]), Title: PlainText(text(detail["title"])), RecruitmentType: programme.recruitmentType, Publisher: "具体招聘主体以官方申请页面为准", PublicStatus: "unknown", StatusEvidence: "当前官方公开列表在列，详情可读；详情状态未明确", WrittenTest: "unknown", SourceURL: link, ApplyURL: link, ApplyLevel: "job", LoginRequired: true, DeadlineTimezone: "unknown", GraduationRules: rules, StudentRequired: programme.studentRequired, MinimumEducation: "unknown", CampaignEvidence: fmt.Sprintf("当前子批次：%s\n父菜单显示（不作为门槛）：%s", condition, parentDisplay), Variants: variants})
	}
	return documents, total, nil
}
