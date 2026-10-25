package ingest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"jobassistant/backend/internal/catalog"
)

var feishuWorkYears = regexp.MustCompile(`([零一二两三四五六七八九十0-9]+)(?:\s*[-~～–—至到]\s*[零一二两三四五六七八九十0-9]+)?\s*年(?:及以上|以上)?[^年，,。；;\n]{0,20}?(?:工作|从业)经验`)
var feishuEnglishYears = regexp.MustCompile(`(?i)\b([0-9]{1,2})(?:\s*(?:to|-)\s*[0-9]{1,2})?\+?\s*years?\s+(?:of\s+)?(?:relevant\s+)?(?:work|professional|industry)\s+experience`)
var feishuInternTitle = regexp.MustCompile(`(?i)\bintern(?:ship)?\b`)
var feishuDays = regexp.MustCompile(`每周\s*(?:至少|不少于|不低于)?\s*(?:到岗|出勤|实习)?\s*(?:至少|不少于|不低于)?\s*([一二三四五六七1-7])\s*天`)

// feishuExperience deliberately excludes project and coursework experience.
func feishuExperience(required string) (int, bool) {
	maximum, explicit := 0, false
	for _, clause := range chaitinClauses.Split(required, -1) {
		if strings.Contains(clause, "无需") || strings.Contains(clause, "不要求") || strings.Contains(clause, "经验不限") {
			continue
		}
		for _, pattern := range []*regexp.Regexp{feishuWorkYears, feishuEnglishYears} {
			for _, index := range pattern.FindAllStringSubmatchIndex(clause, -1) {
				value := clause[index[2]:index[3]]
				n, err := strconv.Atoi(value)
				if value == "零" {
					n, err = 0, nil
				}
				if err != nil {
					var ok bool
					n, ok = chaitinNumber(value)
					if !ok {
						return 100, true
					}
				}
				prefix := strings.TrimSpace(clause[:index[0]])
				matched := clause[index[0]:index[1]]
				upper := strings.Contains(matched, "以内") || strings.Contains(matched, "以下")
				for _, word := range []string{"不超过", "不多于", "最多", "至多", "少于", "小于"} {
					upper = upper || strings.HasSuffix(prefix, word)
				}
				if upper {
					n = 0
				}
				maximum, explicit = max(maximum, n), true
			}
		}
	}
	return maximum, explicit
}

func feishuCampaign(job map[string]any, websiteID string) string {
	if id := text(object(job["job_subject"])["id"]); feishuID.MatchString(id) {
		return "feishu:" + id
	}
	return "feishu:" + websiteID
}

func feishuDirection(job map[string]any) string {
	title := PlainText(text(job["title"]))
	if nontechnicalTitle(title) {
		return "unknown"
	}
	for _, word := range []string{"经理", "总监", "机械", "结构设计", "电气", "硬件测试", "广告投放", "数据运营", "数据标注", "生产", "工艺"} {
		if strings.Contains(title, word) {
			return "unknown"
		}
	}
	lower := strings.ToLower(title)
	if strings.Contains(lower, "ai-infra") || strings.Contains(lower, "ai infra") || strings.Contains(title, "可观测性") {
		return "operations_sre"
	}
	if strings.Contains(title, "数据管道") {
		return "data"
	}
	if direction := Direction(title, ""); direction != "unknown" {
		return direction
	}
	return mokaDirection(title)
}

func feishuRecruitment(job map[string]any, required string) (string, string) {
	typeInfo := object(job["recruit_type"])
	name := strings.ToLower(text(typeInfo["name"]) + " " + text(typeInfo["en_name"]))
	parent := object(typeInfo["parent"])
	parentName := strings.ToLower(text(parent["name"]) + " " + text(parent["en_name"]))
	if strings.Contains(name, "实习") || strings.TrimSpace(text(typeInfo["en_name"])) == "Intern" {
		return "internship", ""
	}
	if strings.Contains(text(job["title"]), "实习") || feishuInternTitle.MatchString(text(job["title"])) {
		return "", "岗位标题与官方实习类型不一致，需人工核验"
	}
	if strings.Contains(parentName, "校招") || strings.Contains(parentName, "campus") || strings.Contains(name, "校招") || strings.Contains(name, "campus") {
		return "campus", ""
	}
	accepts := false
	for _, phrase := range []string{"接受应届", "欢迎应届", "应届毕业生", "经验不限", "无需工作经验", "no work experience required"} {
		accepts = accepts || strings.Contains(strings.ToLower(required), phrase)
	}
	if years, explicit := feishuExperience(required); explicit && years <= 1 {
		accepts = true
	}
	if !accepts {
		return "", "社招接受应届或最多一年经验的依据不足，需人工核验"
	}
	return "early_career", ""
}

func feishuLocations(job map[string]any) []catalog.Location {
	places := []catalog.Location{}
	seen := map[string]bool{}
	add := func(place catalog.Location) {
		if !seen[place.Code] {
			seen[place.Code] = true
			places = append(places, place)
		}
	}
	for _, raw := range array(object(job["job_post_info"])["address_list"]) {
		entry := object(raw)
		city, province, country := object(entry["city"]), object(entry["state"]), object(entry["country"])
		cityName, countryName := PlainText(text(city["name"])), PlainText(text(country["name"]))
		if cityName == "" {
			continue
		}
		if countryName == "中国大陆" || countryName == "中国" || text(country["country_code"]) == "CN_1" {
			for _, place := range Locations(cityName) {
				place.Country = "中国"
				place.Raw = fmt.Sprintf("官方地点：%s/%s/%s", countryName, text(province["name"]), cityName)
				add(place)
			}
		} else if countryName != "" {
			add(catalog.Location{Code: "feishu:" + text(country["country_code"]) + ":" + text(city["city_code"]), Kind: "overseas", Country: countryName, Province: text(province["name"]), City: cityName, Raw: "官方地点：" + countryName + "/" + cityName})
		}
	}
	if len(places) == 0 {
		for _, raw := range array(job["city_list"]) {
			for _, place := range Locations(PlainText(text(object(raw)["name"]))) {
				add(place)
			}
		}
	}
	return places
}

func feishuDocument(site feishuSite, websiteID string, job map[string]any) (catalog.Document, string, error) {
	id, title := text(job["id"]), PlainText(text(job["title"]))
	description, requirement := PlainText(text(job["description"])), PlainText(text(job["requirement"]))
	if !feishuID.MatchString(id) || title == "" {
		return catalog.Document{}, "", ErrSchema
	}
	for _, key := range []string{"description", "requirement"} {
		if raw, present := job[key]; present && raw != nil {
			if _, ok := raw.(string); !ok {
				return catalog.Document{}, "", ErrSchema
			}
		}
	}
	if description == "" || requirement == "" {
		return catalog.Document{}, "官方未披露完整职责或任职条件，保留待人工核验", nil
	}
	direction := feishuDirection(job)
	if direction == "unknown" {
		return catalog.Document{}, "尚未确认属于计算机技术岗位，需人工核验", nil
	}
	if direction == "test" {
		software := false
		for _, word := range []string{"软件", "接口", "脚本", "代码", "python", "software", "c++", "测试开发", "服务器", "客户端"} {
			software = software || strings.Contains(strings.ToLower(description+"\n"+requirement), word)
		}
		if !software {
			return catalog.Document{}, "测试职责未证实属于软件技术方向，需人工核验", nil
		}
	}
	required, preferred := mokaSplitQualifications(requirement)
	if required == "" {
		return catalog.Document{}, "官方未披露可核实的必要条件，需人工核验", nil
	}
	years, _ := feishuExperience(required)
	if years > 1 {
		return catalog.Document{}, "任职条件要求超过一年工作经验，不属于本版范围", nil
	}
	kind, reason := feishuRecruitment(job, required)
	if reason != "" {
		return catalog.Document{}, reason, nil
	}
	if years > 0 {
		required += fmt.Sprintf("\n官方工作经验最低要求：至少%d年工作经验", years)
	}
	// Expose explicit text floors in the existing matcher's canonical vocabulary.
	// Keep the complete original requirement unchanged in campaign evidence.
	if degree := mokaDegree(required); degree != "" {
		required += "\n官方最低学历：" + degree + "及以上"
	}
	student, education := "unknown", "unknown"
	var days, months *int
	for _, clause := range chaitinClauses.Split(required, -1) {
		if strings.Contains(clause, "本科及以上") || strings.Contains(clause, "本科以上") {
			education = "bachelor_or_above"
		}
		if kind != "internship" {
			continue
		}
		if strings.Contains(clause, "在读") || strings.Contains(clause, "在校") {
			student = "yes"
		}
		if match := feishuDays.FindStringSubmatch(clause); len(match) > 1 {
			if n, ok := chaitinNumber(match[1]); ok && n > 0 && n <= 7 && (days == nil || n > *days) {
				days = &n
			}
		}
		if strings.Contains(clause, "实习") {
			for _, match := range chaitinMonths.FindAllStringSubmatch(clause, -1) {
				value := match[1]
				if value == "" {
					value = match[2]
				}
				if n, ok := chaitinNumber(value); ok && n > 0 && n <= 60 && (months == nil || n > *months) {
					months = &n
				}
			}
		}
	}
	// The existing matcher reads higher floors from requirements; its only
	// structured education enum is bachelor_or_above. Do not invent enum values.
	if strings.Contains(required, "硕士") || strings.Contains(required, "博士") || strings.Contains(required, "研究生") {
		education = "unknown"
	}
	link := site.origin + "/" + site.path + "/position/" + id + "/detail"
	subject := object(job["job_subject"])
	campaign := PlainText(text(object(subject["name"])["zh_cn"]))
	if campaign == "" {
		campaign = "官方校园、实习与低经验招聘"
	}
	evidence := fmt.Sprintf("官方招聘站：%s/%s；tenant=%s；website_id=%s\nrecruit_type=%s；parent=%s；subject_id=%s；subject=%s\n未据届数标签推断毕业日期、截止或笔试。\n完整官方职责：\n%s\n完整官方要求：\n%s", site.origin, site.path, site.tenant, websiteID, text(object(job["recruit_type"])["name"]), text(object(object(job["recruit_type"])["parent"])["name"]), text(subject["id"]), campaign, description, requirement)
	variant := catalog.Variant{Key: id, Name: title, Direction: direction, Responsibilities: description, Requirements: required, Preferred: preferred, Major: ParseMajor(required), Locations: feishuLocations(job), Departments: []catalog.DepartmentGroup{}}
	return catalog.Document{ExternalID: id, CompanyCode: site.company, SourceCode: site.source, CampaignKey: feishuCampaign(job, websiteID), CampaignName: campaign, Title: title, RecruitmentType: kind, Publisher: "招聘主体及部门以官方岗位正文为准", PublicStatus: "open", StatusEvidence: "当前官方完整公开列表与同 ID 详情一致，channel_online_status=1", WrittenTest: "unknown", SourceURL: link, ApplyURL: link, ApplyLevel: "job", LoginRequired: true, DeadlineTimezone: "unknown", GraduationRules: []catalog.GraduationRule{}, StudentRequired: student, MinimumEducation: education, MinimumInternshipMonths: months, DaysPerWeek: days, CampaignEvidence: evidence, Variants: []catalog.Variant{variant}}, "", nil
}
