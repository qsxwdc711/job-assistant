package ingest

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"jobassistant/backend/internal/catalog"
)

var graduationPreference = regexp.MustCompile(`\d{4}届优先`)

// CMB reads campus and internship positions without inheriting campus-announcement conditions.
func (r *Reader) CMB(ctx context.Context) ([]catalog.Document, int, error) {
	headers := map[string]string{"X-B3-BusinessId": "LZ4101CMBRecruitmentPCFront", "Referer": r.CMBBase + "/campus/home", "Origin": r.CMBBase}
	documents := []catalog.Document{}
	fetched := 0
	for _, category := range []struct{ id, kind, name string }{{"96574F8D-C7ED-4772-AE7C-BAC896D190C1", "campus", "校园招聘（具体年度未公布）"}, {"DF94FD6D-26D3-4A19-9E69-577C4BA1DE82", "internship", "实习招聘（具体年度未公布）"}} {
		items := []map[string]any{}
		seen := map[string]bool{}
		total := -1
		for page := 1; page <= 100; page++ {
			input := map[string]any{"orgIdList": []string{}, "locationIdList": []string{}, "keywords": "", "pageIndex": page, "pageSize": 10, "recruitmentTypeId": category.id}
			response, err := r.json(ctx, http.MethodPost, r.CMBBase+"/api/campusRecruitmentWebsite/job/getList", input, headers)
			if err != nil {
				return nil, fetched, err
			}
			payload, err := cmbData(response)
			if err != nil {
				return nil, fetched, err
			}
			n, err := count(payload["total"])
			if err != nil {
				return nil, fetched, err
			}
			if total < 0 {
				total = n
			}
			if n != total {
				return nil, fetched, ErrSchema
			}
			entries := array(payload["data"])
			if total == 0 && len(entries) == 0 {
				break
			}
			if len(entries) == 0 {
				return nil, fetched, ErrSchema
			}
			for _, entry := range entries {
				item := object(entry)
				id := text(item["publishGID"])
				if id == "" || seen[id] {
					return nil, fetched, ErrSchema
				}
				seen[id] = true
				items = append(items, item)
			}
			if len(items) >= total {
				break
			}
		}
		if len(items) != total {
			return nil, fetched, ErrSchema
		}
		fetched += total
		for _, item := range items {
			id := text(item["publishGID"])
			response, err := r.json(ctx, http.MethodGet, r.CMBBase+"/api/campusRecruitmentWebsite/job/getDetail?publishId="+url.QueryEscape(id), nil, headers)
			if err != nil {
				return nil, fetched, err
			}
			detail, err := cmbData(response)
			if err != nil {
				return nil, fetched, err
			}
			if text(detail["publishGID"]) != id {
				return nil, fetched, ErrSchema
			}
			title := PlainText(text(detail["jobDisplay"]))
			duties := PlainText(text(detail["jobResponsibility"]))
			requirements := PlainText(text(detail["jobRequirement"]))
			if title == "" || requirements == "" {
				return nil, fetched, ErrSchema
			}
			direction := Direction(title, duties)
			if direction == "unknown" {
				r.Exclusions = append(r.Exclusions, catalog.Exclusion{CampaignKey: "type:" + category.id + ":unknown-year", ExternalID: id, Reason: "岗位名称与职责未确认属于计算机方向，需人工复核"})
				continue
			}
			date := text(detail["expiredOn"])
			if date != "" {
				if _, err := time.Parse("2006-01-02", date); err != nil {
					return nil, fetched, ErrSchema
				}
			}
			student, education := "unknown", "unknown"
			if strings.Contains(requirements, "在校") {
				student = "yes"
			}
			if strings.Contains(requirements, "本科及以上") {
				education = "bachelor_or_above"
			}
			var months, days *int
			if strings.Contains(requirements, "不少于一个月") || strings.Contains(requirements, "至少一个月") {
				v := 1
				months = &v
			}
			if strings.Contains(requirements, "每周出勤5天") || strings.Contains(requirements, "每周5天") {
				v := 5
				days = &v
			}
			link := "https://career.cmbchina.com/positionDetail/school?publishId=" + url.QueryEscape(id)
			documents = append(documents, catalog.Document{ExternalID: id, CompanyCode: "cmb", SourceCode: "cmb-campus", CampaignKey: "type:" + category.id + ":unknown-year", CampaignName: category.name, Title: title, RecruitmentType: category.kind, Publisher: "招商银行" + text(detail["branchCodeName"]), PublicStatus: "open", StatusEvidence: "当前官方岗位列表在列且具体详情成功", WrittenTest: "unknown", SourceURL: link, ApplyURL: link, ApplyLevel: "job", LoginRequired: true, DeadlineDate: date, DeadlineTimezone: "unknown", GraduationRules: []catalog.GraduationRule{}, GraduationPreference: graduationPreference.FindString(requirements), StudentRequired: student, MinimumEducation: education, MinimumInternshipMonths: months, DaysPerWeek: days, CampaignEvidence: "具体岗位条件；不自动继承全职校园公告的毕业范围或笔试规则", Variants: []catalog.Variant{{Key: "main", Name: title, Direction: direction, Responsibilities: duties, Requirements: requirements, Major: ParseMajor(requirements), Locations: Locations(text(detail["locationName"])), Departments: []catalog.DepartmentGroup{}}}})
		}
	}
	return documents, fetched, nil
}
