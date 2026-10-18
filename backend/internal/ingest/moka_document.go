package ingest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"jobassistant/backend/internal/catalog"
)

var mokaExperience = regexp.MustCompile(`([一二三四五六七八九十0-9]+)\s*(?:年(?:及以上|以上)?(?:的)?(?:相关)?(?:工作|开发|研发|项目)?经验|年以上)`)
var mokaMinimumMonths = regexp.MustCompile(`(?:至少|最短需承诺|不少于)\s*([一二三四五六七八九十0-9]+)\s*个?月`)
var mokaWeeklyDays = regexp.MustCompile(`每周(?:至少|不少于)?\s*([一二三四五六七1-7])\s*天`)
var mokaClause = regexp.MustCompile(`[。；;\n]`)
var mokaExperienceClause = regexp.MustCompile(`[，,。；;\n]`)
var mokaAI = regexp.MustCompile(`(?i)\bai\b|大模型|算法`)

func mokaNumber(value string) (int, bool) {
	if n, err := strconv.Atoi(value); err == nil && n > 0 && n <= 99 {
		return n, true
	}
	n, ok := map[string]int{"一": 1, "二": 2, "两": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9, "十": 10, "十一": 11, "十二": 12}[value]
	return n, ok
}

func mokaDirection(title string) string {
	title = strings.ToLower(title)
	if (strings.Contains(title, "运营") && !strings.Contains(title, "开发") && !strings.Contains(title, "算法") && !strings.Contains(title, "研究") && !strings.Contains(title, "工程师")) || (strings.Contains(title, "产品") && !strings.Contains(title, "开发") && !strings.Contains(title, "研发") && !strings.Contains(title, "工程师")) {
		return "unknown"
	}
	for _, word := range []string{"美术", "特效", "视频", "动画", "音效", "ui设计", "gui设计", "销售", "市场", "营销", "人力", "招聘", "财务", "行政", "硬件测试", "可靠性测试", "声学测试", "质量检验", "项目经理", "产品经理", "售前"} {
		if strings.Contains(title, word) {
			return "unknown"
		}
	}
	if mokaAI.MatchString(title) {
		return "algorithm_ai"
	}
	// Explicit role families take precedence over an implementation language.
	if direction := Direction(title, ""); direction != "unknown" {
		return direction
	}
	for _, word := range []string{"java", "c++", "c#", "golang", "服务端", "引擎开发", "游戏开发", "程序员", "程序开发"} {
		if strings.Contains(title, word) {
			return "backend"
		}
	}
	return "unknown"
}

func mokaRecruitment(site mokaSite, detail map[string]any, description string) (string, string) {
	commitment := text(detail["commitment"])
	title := text(detail["title"])
	if commitment != "实习" && strings.Contains(title, "实习") {
		return "", "岗位标题与招聘类型冲突，需人工确认"
	}
	if commitment == "实习" {
		return "internship", ""
	}
	if commitment != "全职" {
		return "", "招聘类型未明确适用于校招、实习或低经验岗位"
	}
	if site.mode == "campus" && text(detail["hireMode"]) == "2" {
		return "campus", ""
	}
	if site.mode != "social" || text(detail["hireMode"]) != "1" {
		return "", "站点与岗位招聘类型不一致，需人工确认"
	}
	accepts := strings.Contains(description, "接受应届") || strings.Contains(description, "欢迎应届") || strings.Contains(description, "应届毕业生") || strings.Contains(description, "经验不限") || strings.Contains(description, "无需工作经验")
	if value, exists := detail["minExperience"]; exists && value != nil {
		n, err := strconv.Atoi(text(value))
		if err != nil || n < 0 {
			return "", "社招最低经验字段无法确认"
		}
		if n > 1 {
			return "", "社招明确要求超过一年经验，不属于本版范围"
		}
		accepts = true
	}
	for _, clause := range mokaExperienceClause.Split(description, -1) {
		if strings.Contains(clause, "优先") || strings.Contains(clause, "加分") {
			continue
		}
		for _, match := range mokaExperience.FindAllStringSubmatch(clause, -1) {
			if n, ok := mokaNumber(match[1]); !ok || n > 1 {
				return "", "社招正文要求超过一年经验，不属于本版范围"
			}
			accepts = true
		}
	}
	if !accepts {
		return "", "社招接受应届或低经验的依据不足，需人工确认"
	}
	return "early_career", ""
}

var mokaCities = map[string]string{
	"1100": "北京", "1200": "天津", "3100": "上海", "5000": "重庆",
	"4401": "广州", "4403": "深圳", "4404": "珠海", "4406": "佛山", "4413": "惠州", "4419": "东莞",
	"5101": "成都", "6101": "西安", "4201": "武汉", "4301": "长沙", "3201": "南京", "3205": "苏州", "3202": "无锡",
	"3301": "杭州", "3302": "宁波", "3401": "合肥", "3701": "济南", "3702": "青岛", "3501": "福州", "3502": "厦门",
	"2101": "沈阳", "2102": "大连", "2201": "长春", "2301": "哈尔滨", "4101": "郑州", "1301": "石家庄",
	"1401": "太原", "1501": "呼和浩特", "3601": "南昌", "4501": "南宁", "5201": "贵阳", "5301": "昆明",
}

func mokaLocation(entry map[string]any) catalog.Location {
	code, country, name := text(entry["cityId"]), text(entry["country"]), PlainText(text(entry["cityName"]))
	if country == "China" || country == "中国" {
		prefix := code
		if len(code) == 6 {
			prefix = code[:4]
			if code[:2] == "11" || code[:2] == "12" || code[:2] == "31" || code[:2] == "50" {
				prefix = code[:2] + "00"
			}
		}
		if city, ok := mokaCities[prefix]; ok {
			place := Locations(city)[0]
			place.Raw = fmt.Sprintf("官方 cityId=%s; cityName=%s", code, name)
			return place
		}
		country = "中国"
	}
	if name == "" {
		name = "未明确"
	}
	if country == "" {
		country = "unknown"
	}
	kind := "unknown"
	if country != "中国" && country != "unknown" {
		kind = "overseas"
	}
	return catalog.Location{Code: "moka:" + country + ":" + code + ":" + name, Kind: kind, Country: country, Province: "unknown", City: name, Raw: fmt.Sprintf("官方 cityId=%s; cityName=%s; country=%s", code, name, country)}
}

func mokaDocument(site mokaSite, detail map[string]any) (catalog.Document, string, error) {
	description := PlainText(text(detail["jobDescription"]))
	if description == "" {
		if strings.HasPrefix(text(detail["title"]), "联培课题-") {
			return catalog.Document{}, "官方联培课题未披露岗位职责及投递条件，暂不作为标准招聘岗位发布，需人工核验", nil
		}
		return catalog.Document{}, "", ErrSchema
	}
	kind, reason := mokaRecruitment(site, detail, description)
	if reason != "" {
		return catalog.Document{}, reason, nil
	}
	id, title := text(detail["id"]), PlainText(text(detail["title"]))
	if mokaDirection(title) == "test" {
		software := false
		for _, term := range []string{"软件", "接口", "代码", "脚本", "测试开发", "python", "pytest"} {
			software = software || strings.Contains(strings.ToLower(description), term)
		}
		if !software {
			return catalog.Document{}, "测试职责未确认属于软件技术方向，需人工复核", nil
		}
	}
	locations := []catalog.Location{}
	placeByID := map[string]catalog.Location{}
	for _, raw := range array(detail["locations"]) {
		entry := object(raw)
		place := mokaLocation(entry)
		locations = append(locations, place)
		placeByID[text(entry["id"])] = place
	}
	departmentNames := map[string]string{}
	for _, raw := range array(detail["departments"]) {
		entry := object(raw)
		departmentNames[text(entry["id"])] = PlainText(text(entry["name"]))
	}
	departmentGroups := []catalog.DepartmentGroup{}
	for i, raw := range array(detail["locDepCampusList"]) {
		entry := object(raw)
		for _, department := range array(entry["departmentIds"]) {
			key := text(department)
			name := departmentNames[key]
			if name == "" {
				continue
			}
			places := []catalog.Location{}
			for _, location := range array(entry["locationIds"]) {
				if place, exists := placeByID[text(location)]; exists {
					places = append(places, place)
				}
			}
			departmentGroups = append(departmentGroups, catalog.DepartmentGroup{Code: fmt.Sprintf("%s:%d", key, i), Name: name, Locations: places})
		}
	}
	qualification := description
	education := PlainText(text(detail["education"]))
	for _, degree := range []string{"本科", "硕士", "博士", "专科"} {
		if education == degree || education == degree+"及以上" {
			qualification += "\n官方学历字段：" + degree + "及以上"
			break
		}
	}
	student := "unknown"
	var months, days *int
	if kind == "internship" {
		for _, clause := range mokaClause.Split(description, -1) {
			if strings.Contains(clause, "优先") || strings.Contains(clause, "不限") || strings.Contains(clause, "无需") {
				continue
			}
			if strings.Contains(clause, "在校学生") || strings.Contains(clause, "在校生") || strings.Contains(clause, "在读") {
				student = "yes"
			}
			if strings.Contains(clause, "实习") {
				for _, match := range mokaMinimumMonths.FindAllStringSubmatch(clause, -1) {
					if n, ok := mokaNumber(match[1]); ok && (months == nil || n > *months) {
						months = &n
					}
				}
			}
			if match := mokaWeeklyDays.FindStringSubmatch(clause); len(match) == 2 {
				if n, ok := mokaNumber(match[1]); ok && n <= 7 && (days == nil || n > *days) {
					days = &n
				}
			}
		}
	}
	link := "https://app.mokahr.com" + site.path + "#/job/" + id
	project := object(detail["projectFolder"])
	evidence := fmt.Sprintf("官方授权 Moka 招聘站：%s\n岗位 hireMode=%s；commitment=%s；学历原字段=%s\n项目原字段：%s（%s）；未据项目名称推断毕业日期或笔试", "https://app.mokahr.com"+site.path, text(detail["hireMode"]), text(detail["commitment"]), education, PlainText(text(project["name"])), text(project["id"]))
	variant := catalog.Variant{Key: id, Name: title, Direction: mokaDirection(title), Responsibilities: description, Requirements: qualification, Preferred: "", Major: ParseMajor(description), Locations: locations, Departments: departmentGroups}
	campaign := "官方校园与实习招聘"
	if site.mode == "social" {
		campaign = "官方实习与低经验招聘"
	}
	return catalog.Document{ExternalID: id, CompanyCode: site.company, SourceCode: site.source, CampaignKey: "moka:" + site.site, CampaignName: campaign, Title: title, RecruitmentType: kind, Publisher: "招聘主体及部门以官方岗位正文为准", PublicStatus: "open", StatusEvidence: "当前官方租户完整列表和同 ID 详情均明确 status=open", WrittenTest: "unknown", SourceURL: link, ApplyURL: link, ApplyLevel: "job", LoginRequired: true, DeadlineTimezone: "unknown", GraduationRules: []catalog.GraduationRule{}, StudentRequired: student, MinimumEducation: "unknown", MinimumInternshipMonths: months, DaysPerWeek: days, CampaignEvidence: evidence, Variants: []catalog.Variant{variant}}, "", nil
}
