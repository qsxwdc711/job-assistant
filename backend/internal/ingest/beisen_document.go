package ingest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"jobassistant/backend/internal/catalog"
)

var (
	beisenClauses      = regexp.MustCompile(`[，,。；;\n]`)
	beisenCohort       = regexp.MustCompile(`(20[0-9]{2})\s*届`)
	beisenYears        = regexp.MustCompile(`([0-9零一二两三四五六七八九十]+)(?:\s*[-~～–—至到]\s*[0-9零一二两三四五六七八九十]+)?\s*年(?:及以上|以上)?[^年，,。；;\n]{0,20}?(?:工作|从业)经验`)
	beisenEnglishYears = regexp.MustCompile(`(?i)\b([0-9]{1,2})(?:\s*(?:to|[-–—])\s*[0-9]{1,2})?\+?\s*years?\s+(?:of\s+)?(?:relevant\s+)?(?:work|professional|industry)\s+experience`)
)

func beisenDirection(job map[string]any) string {
	title, duty := PlainText(text(job["JobAdName"])), strings.ToLower(PlainText(text(job["Duty"])))
	for _, word := range []string{"营销", "销售", "市场", "采购", "财务", "人力", "行政", "门店", "运营", "产品经理", "设计", "食品", "质量检验"} {
		if strings.Contains(title, word) {
			return "unknown"
		}
	}
	if strings.Contains(title, "数据开发") {
		return "data"
	}
	if direction := Direction(title, ""); direction != "unknown" {
		return direction
	}
	if strings.Contains(title, "系统实施") && (strings.Contains(duty, "系统配置") || strings.Contains(duty, "系统运行")) && strings.Contains(PlainText(text(job["Require"])), "计算机") {
		return "operations_sre"
	}
	if strings.Contains(strings.ToLower(title), "ai") && (strings.Contains(duty, "机器学习") || strings.Contains(duty, "大模型") || strings.Contains(duty, "算法")) && (strings.Contains(duty, "python") || strings.Contains(duty, "模型") || strings.Contains(duty, "代码")) {
		return "algorithm_ai"
	}
	return "unknown"
}

func beisenQualifications(value string) (string, string) {
	hard, preferred := mokaSplitQualifications(PlainText(value))
	trimLines := func(value string) string {
		lines := []string{}
		for _, line := range strings.Split(value, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	}
	return trimLines(hard), trimLines(preferred)
}

// beisenExperience recognizes employment floors, never project or coursework years.
func beisenExperience(value string) (int, error) {
	minimum := 0
	for _, clause := range beisenClauses.Split(value, -1) {
		if mokaPreferred(clause) || strings.Contains(clause, "经验不限") || strings.Contains(clause, "无需") || strings.Contains(clause, "不要求") {
			continue
		}
		for _, pattern := range []*regexp.Regexp{beisenYears, beisenEnglishYears} {
			for _, match := range pattern.FindAllStringSubmatch(clause, -1) {
				n, err := strconv.Atoi(match[1])
				if err != nil {
					var ok bool
					n, ok = map[string]int{"零": 0, "一": 1, "二": 2, "两": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9, "十": 10, "十一": 11, "十二": 12}[match[1]]
					if !ok {
						return 0, ErrSchema
					}
				}
				minimum = max(minimum, n)
			}
		}
	}
	return minimum, nil
}

func beisenMajor(original, hard, preferred string) catalog.MajorRequirement {
	if required := ParseMajor(hard); required.Kind != "unknown" {
		full := ParseMajor(original)
		if strings.Contains(full.Evidence, required.Evidence) {
			// Preserve the full original line without promoting its bonus clauses.
			full.Kind, full.Tags = required.Kind, required.Tags
			return full
		}
		return required
	}
	bonus := ParseMajor(preferred)
	if bonus.Kind != "unknown" {
		bonus.Kind = "preferred"
	}
	return bonus
}

func beisenLocations(value any) ([]catalog.Location, error) {
	entries, ok := value.([]any)
	if !ok || len(entries) == 0 {
		return nil, ErrSchema
	}
	locations := []catalog.Location{}
	seen := map[string]bool{}
	for _, raw := range entries {
		place, ok := raw.(string)
		if !ok || strings.TrimSpace(place) == "" {
			return nil, ErrSchema
		}
		location := catalog.Location{Code: "raw:" + place, Kind: "unknown", Country: "unknown", Province: "unknown", City: place, Raw: place}
		if place == "国外" || place == "海外" || place == "境外" {
			location.Kind, location.City, location.Code = "overseas", "未明确", "overseas"
		}
		for _, part := range strings.Split(place, "·") {
			candidates := Locations(part)
			if len(candidates) == 1 && candidates[0].Kind == "domestic" {
				location = candidates[0]
				location.Raw = place
				break
			}
		}
		if !seen[location.Code] {
			seen[location.Code] = true
			locations = append(locations, location)
		}
	}
	return locations, nil
}

func beisenDocument(site beisenSite, job map[string]any) (catalog.Document, string, error) {
	direction := beisenDirection(job)
	if direction == "unknown" {
		return catalog.Document{}, "岗位职责未确认属于计算机技术方向，保留待复核", nil
	}
	if !beisenIdentity(job) || text(job["Status"]) != "1" {
		return catalog.Document{}, "官方岗位未处于开放状态，保留待复核", nil
	}
	title, duty, originalRequirements := PlainText(text(job["JobAdName"])), PlainText(text(job["Duty"])), PlainText(text(job["Require"]))
	if duty == "" || originalRequirements == "" {
		return catalog.Document{}, "", ErrSchema
	}
	if text(job["Kind"]) != "全职" || strings.Contains(title, "实习") {
		return catalog.Document{}, "校园站招聘类型与岗位全职/实习依据不一致，保留待复核", nil
	}
	hard, preferred := beisenQualifications(originalRequirements)
	titleYears, requirementYears := beisenCohort.FindAllStringSubmatch(title, -1), beisenCohort.FindAllStringSubmatch(hard, -1)
	if len(titleYears) == 1 && len(requirementYears) > 0 {
		matched := false
		for _, year := range requirementYears {
			matched = matched || titleYears[0][1] == year[1]
		}
		if !matched {
			return catalog.Document{}, "标题与任职要求的毕业届数冲突，保留原文待人工核验", nil
		}
	}
	degree := PlainText(text(job["Degree"]))
	if degree != "" {
		known := false
		for _, word := range []string{"博士", "硕士", "研究生", "本科", "专科", "大专", "不限", "无要求"} {
			known = known || strings.Contains(degree, word)
		}
		if !known {
			return catalog.Document{}, "", fmt.Errorf("beisen public degree field: %w", ErrSchema)
		}
		condition := degree
		switch degree {
		case "本科", "硕士", "博士", "专科", "大专":
			condition += "及以上"
		case "硕士研究生", "研究生":
			condition = "硕士及以上"
		}
		hard += "\n官方学历字段：" + condition
	}
	experience := PlainText(text(job["YearsOfWorking"]))
	if experience != "" {
		if !strings.Contains(experience, "年") && !strings.Contains(experience, "不限") && !strings.Contains(experience, "应届") && !strings.Contains(experience, "无经验") {
			return catalog.Document{}, "", fmt.Errorf("beisen public experience field: %w", ErrSchema)
		}
		// The public field is a requirement even when its display flag is false.
		hard += "\n官方工作经验字段：" + experience + "工作经验"
	}
	years, err := beisenExperience(hard)
	if err != nil {
		return catalog.Document{}, "", err
	}
	if years > 1 {
		return catalog.Document{}, "官方任职要求存在超过一年硬性经验门槛，不属于本版范围", nil
	}
	if years == 1 {
		hard += "\n官方经验要求：至少1年工作经验（原文见上方及批次依据）"
	}
	locations, err := beisenLocations(job["LocNames"])
	if err != nil {
		return catalog.Document{}, "", err
	}
	education := "unknown"
	if strings.Contains(hard, "本科及以上") || strings.Contains(hard, "本科以上") || strings.Contains(hard, "本科学历") {
		education = "bachelor_or_above"
	}
	if strings.Contains(hard, "硕士") || strings.Contains(hard, "博士") || strings.Contains(hard, "研究生") {
		education = "unknown" // The higher floor stays in per-direction hard requirements.
	}
	id := text(job["Id"])
	link := site.origin + "/campus/detail?jobAdId=" + id
	evidence := fmt.Sprintf("企业官网公开链接当前北森招聘站：%s/campus/jobs\n公开租户ID=%d；列表与详情 CategoryId=2；Status=1；Kind=%s；数字岗位ID=%s。\n官方学历字段=%s；官方工作经验字段=%s；工作地点=%v。\n未按网页展示的届数推算毕业日期；未按发布或修改时间推算截止日期或笔试。\n完整官方工作职责：\n%s\n完整官方任职要求：\n%s", site.origin, site.tenantID, text(job["Kind"]), text(job["JobAdId"]), degree, experience, job["LocNames"], duty, originalRequirements)
	variant := catalog.Variant{Key: id, Name: title, Direction: direction, Responsibilities: duty, Requirements: strings.TrimSpace(hard), Preferred: preferred, Major: beisenMajor(originalRequirements, hard, preferred), Locations: locations, Departments: []catalog.DepartmentGroup{}}
	return catalog.Document{ExternalID: id, CompanyCode: site.company, SourceCode: site.source, CampaignKey: fmt.Sprintf("beisen:%d:2", site.tenantID), CampaignName: "官方校园招聘", Title: title, RecruitmentType: "campus", Publisher: site.companyName + "；具体招聘主体以官方岗位为准", PublicStatus: "open", StatusEvidence: "官方公开完整分页列表与同ID详情均为Status=1，身份和职责一致；完成后再次核对完整列表", WrittenTest: "unknown", SourceURL: link, ApplyURL: link, ApplyLevel: "job", LoginRequired: true, DeadlineTimezone: "unknown", GraduationRules: []catalog.GraduationRule{}, StudentRequired: "unknown", MinimumEducation: education, CampaignEvidence: evidence, Variants: []catalog.Variant{variant}}, "", nil
}
