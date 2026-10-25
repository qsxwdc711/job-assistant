package ingest

import (
	"fmt"
	"regexp"
	"sort"
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
var mokaInternshipTitle = regexp.MustCompile(`(?i)\bintern(?:ship)?\b|实习`)

func mokaNumber(value string) (int, bool) {
	if n, err := strconv.Atoi(value); err == nil && n > 0 && n <= 99 {
		return n, true
	}
	n, ok := map[string]int{"一": 1, "二": 2, "两": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9, "十": 10, "十一": 11, "十二": 12}[value]
	return n, ok
}

func mokaDirection(title string) string {
	title = strings.ToLower(title)
	if nontechnicalTitle(title) {
		return "unknown"
	}
	for _, group := range []struct {
		direction string
		terms     []string
	}{
		{"embedded", []string{"embedded software", "firmware", "autosar"}},
		{"mobile", []string{"app开发", "app 开发", "mobile developer", "mobile engineer"}},
		{"operations_sre", []string{"it部", "it工程", "it support engineer", "it operations engineer", "devops engineer"}},
		{"test", []string{"software test", "test automation", "sdet"}},
		{"data", []string{"data engineer", "data scientist", "data analyst"}},
		{"algorithm_ai", []string{"algorithm engineer", "machine learning engineer", "deep learning engineer"}},
		{"backend", []string{"software engineer", "software developer", "software development", "backend engineer", "backend developer", "server developer", "full stack engineer"}},
	} {
		for _, term := range group.terms {
			if strings.Contains(title, term) {
				return group.direction
			}
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
	internshipTitle := strings.Contains(title, "实习")
	if site.structuredQualifications {
		internshipTitle = mokaInternshipTitle.MatchString(title)
	}
	if site.structuredQualifications && detail["commitment"] == nil && internshipTitle && text(detail["hireMode"]) == "2" {
		return "internship", ""
	}
	if commitment != "实习" && internshipTitle {
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
	id, title := text(detail["id"]), PlainText(text(detail["title"]))
	direction := mokaDirection(title)
	if direction == "unknown" {
		return catalog.Document{}, "当前岗位标题未确认属于计算机技术方向，需人工复核", nil
	}
	description := PlainText(text(detail["jobDescription"]))
	if description == "" {
		if strings.HasPrefix(text(detail["title"]), "联培课题-") {
			return catalog.Document{}, "官方联培课题未披露岗位职责及投递条件，暂不作为标准招聘岗位发布，需人工核验", nil
		}
		return catalog.Document{}, "", ErrSchema
	}
	if site.structuredQualifications && !mokaComputerDuties(direction, title, description) {
		if strings.Contains(title, "芯片") || strings.Contains(title, "硬件") {
			return catalog.Document{}, "芯片或硬件岗位未披露明确的软件开发、测试或工具链实现职责，需人工复核", nil
		}
		return catalog.Document{}, "岗位实际职责未确认属于计算机技术方向，不因安全、运维或 AI 产品关键词直接纳入", nil
	}
	qualification, preferred, fieldEvidence := description, "", ""
	if site.structuredQualifications {
		var reason string
		qualification, preferred, fieldEvidence, reason = mokaQualifications(detail, description)
		if reason != "" {
			return catalog.Document{}, reason, nil
		}
	}
	kind, reason := mokaRecruitment(site, detail, qualification)
	if reason != "" {
		return catalog.Document{}, reason, nil
	}
	if direction == "test" {
		software := false
		for _, term := range []string{"软件", "接口", "代码", "脚本", "测试开发", "python", "pytest", "software", "automation", "unit test", "c++", "unity", "bug", "客户端", "服务器"} {
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
	if site.structuredQualifications && len(locations) == 0 {
		for _, field := range mokaPublicFields(detail) {
			if strings.Contains(field.name, "工作城市") || field.name == "工作地点" {
				locations = append(locations, Locations(field.value)...)
			}
		}
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
	education := PlainText(text(detail["education"]))
	if !site.structuredQualifications {
		for _, degree := range []string{"本科", "硕士", "博士", "专科"} {
			if education == degree || education == degree+"及以上" {
				qualification += "\n官方学历字段：" + degree + "及以上"
				break
			}
		}
	}
	student := "unknown"
	var months, days *int
	if kind == "internship" {
		studentText := description
		if site.structuredQualifications {
			studentText = qualification
		}
		for _, clause := range mokaClause.Split(studentText, -1) {
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
	if fieldEvidence != "" {
		evidence += "\n" + fieldEvidence
	}
	variant := catalog.Variant{Key: id, Name: title, Direction: direction, Responsibilities: description, Requirements: qualification, Preferred: preferred, Major: ParseMajor(description), Locations: locations, Departments: departmentGroups}
	campaign := "官方校园与实习招聘"
	if site.mode == "social" {
		campaign = "官方实习与低经验招聘"
	}
	return catalog.Document{ExternalID: id, CompanyCode: site.company, SourceCode: site.source, CampaignKey: "moka:" + site.site, CampaignName: campaign, Title: title, RecruitmentType: kind, Publisher: "招聘主体及部门以官方岗位正文为准", PublicStatus: "open", StatusEvidence: "当前官方租户完整列表和同 ID 详情均明确 status=open", WrittenTest: "unknown", SourceURL: link, ApplyURL: link, ApplyLevel: "job", LoginRequired: true, DeadlineTimezone: "unknown", GraduationRules: []catalog.GraduationRule{}, StudentRequired: student, MinimumEducation: "unknown", MinimumInternshipMonths: months, DaysPerWeek: days, CampaignEvidence: evidence, Variants: []catalog.Variant{variant}}, "", nil
}

type mokaPublicField struct{ name, value string }

var mokaQualificationHeading = regexp.MustCompile(`(?im)^[ \t]*(?:[一二三四五六七八九十0-9]+[、.．)）][ \t]*)?(?:任职要求|任职资格|职位要求|职位资格|招聘要求|qualifications|requirements)[：:\s]*`)
var mokaFallbackQualificationHeading = regexp.MustCompile(`(?m)^[ \t]*(?:[一二三四五六七八九十0-9]+[、.．)）][ \t]*)?岗位要求[：:\s]*`)

// mokaComputerDuties disambiguates software, security and IT operations from
// chip physical design, hardware tests, physical safety and service management.
// A degree preference or familiarity with office/AI tools alone is not a duty.
func mokaComputerDuties(direction, title, description string) bool {
	if strings.Contains(title, "产品") && !strings.Contains(title, "开发") && !strings.Contains(title, "研发") {
		return false
	}
	chipOrHardware := (direction == "backend" || direction == "test") && (strings.Contains(title, "芯片") || strings.Contains(title, "硬件"))
	if direction != "security" && direction != "operations_sre" && !chipOrHardware {
		return true
	}
	duties := description
	for _, heading := range []*regexp.Regexp{mokaQualificationHeading, mokaFallbackQualificationHeading} {
		if index := heading.FindStringIndex(duties); index != nil {
			duties = duties[:index[0]]
			break
		}
	}
	duties = strings.ToLower(duties)
	terms := []string{"linux", "kubernetes", "k8s", "docker", "服务器", "数据库", "操作系统", "网络设备", "信息系统", "应用系统", "软件系统", "it系统", "网络运维", "计算机网络", "云平台", "云服务", "devops", "自动化脚本", "软件部署", "用户权限", "sap", "erp", "mes系统", "vllm", "tensorrt", "mlops", "cicd", "训练基础设施", "推理服务", "组策略", "gpo", "软件分发", "软件版本管控"}
	if direction == "security" {
		terms = []string{"网络安全", "信息安全", "数据安全", "软件安全", "渗透", "网络攻击", "安全漏洞", "代码审计", "防火墙", "密码学", "加密算法", "软件开发", "算法开发", "算法实现", "c++", "python", "matlab", "simulink", "云平台安全", "云上安全", "云资产漏洞", "漏洞修复", "密钥管理", "安全启动", "tee", "hsm", "深度学习", "模型训练"}
	}
	if chipOrHardware {
		terms = []string{"软件开发", "软件测试", "工具链开发", "脚本开发", "软件框架", "驱动开发", "固件开发", "接口测试", "software development", "software test", "firmware development", "driver development"}
	}
	for _, term := range terms {
		if strings.Contains(duties, term) {
			return true
		}
	}
	return false
}

// mokaPublicFields retains applicant-facing conditions, not ATS workflow fields.
// Sorting by the original stable field ID keeps content identity deterministic.
func mokaPublicFields(detail map[string]any) []mokaPublicField {
	fields := object(detail["customFields"])
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := []mokaPublicField{}
	for _, key := range keys {
		field := object(fields[key])
		name, value := PlainText(text(field["name"])), PlainText(text(field["value"]))
		if name == "" || value == "" {
			continue
		}
		for _, term := range []string{"学历", "学位", "工作经验", "工作城市", "工作地点", "招聘项目", "毕业", "在校", "实习时长", "出勤", "公司（中文）", "事业部", "部门名称", "Legal entity"} {
			if strings.Contains(name, term) {
				result = append(result, mokaPublicField{name: name, value: value})
				break
			}
		}
	}
	return result
}

var mokaFieldExperience = regexp.MustCompile(`(?i)^\s*([0-9]{1,2}|[一二三四五六七八九十两]+)\s*(?:[-~～至到]\s*[0-9]{1,2}\s*)?(?:年|years?|yrs?)`)
var mokaEmploymentExperience = regexp.MustCompile(`([一二三四五六七八九十两0-9]+)\s*(?:[-~～至到]\s*[一二三四五六七八九十两0-9]+\s*)?年[^，,。；;\n]{0,24}?(?:工作|从业)经验`)
var mokaEnglishWorkExperience = regexp.MustCompile(`(?i)([1-9][0-9]?)\+?\s*years?\s+(?:of\s+)?(?:relevant\s+)?(?:professional|work|industry)\s+experience`)
var mokaPreferenceHeading = regexp.MustCompile(`^(?:加分项|优先条件|加分条件|preferred qualifications|nice to have|bonus)(?:[\s（(:：].*)?$`)

func mokaPreferred(value string) bool {
	value = strings.ToLower(value)
	for _, word := range []string{"优先", "加分", "preferred", "nice to have", "bonus"} {
		if strings.Contains(value, word) {
			return true
		}
	}
	return false
}

// mokaSplitQualifications preserves section-level preferences, whose individual
// lines often omit the word "preferred" but must never become hard exclusions.
func mokaSplitQualifications(description string) (string, string) {
	required, preferred := []string{}, []string{}
	preferenceSection := false
	for _, line := range strings.Split(description, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		heading := strings.Trim(lower, " ：:•*-0123456789.、")
		if mokaPreferenceHeading.MatchString(heading) {
			preferenceSection = true
			preferred = append(preferred, line)
			continue
		}
		if heading == "任职要求" || heading == "岗位要求" || heading == "基本要求" || heading == "岗位职责" || heading == "工作职责" || heading == "requirements" || heading == "responsibilities" {
			preferenceSection = false
		}
		if preferenceSection {
			preferred = append(preferred, line)
			continue
		}
		for _, clause := range mokaExperienceClause.Split(line, -1) {
			if strings.TrimSpace(clause) == "" {
				continue
			}
			if mokaPreferred(clause) {
				preferred = append(preferred, clause)
			} else {
				required = append(required, clause)
			}
		}
	}
	return strings.Join(required, "\n"), strings.Join(preferred, "\n")
}

func mokaDegree(value string) string {
	value = strings.ToLower(value)
	for _, degree := range []struct{ result, chinese, english string }{{"专科", "专科", "associate"}, {"本科", "本科", "bachelor"}, {"硕士", "硕士", "master"}, {"博士", "博士", "doctor"}} {
		if strings.Contains(value, degree.chinese) || strings.Contains(value, degree.english) {
			return degree.result
		}
	}
	if strings.Contains(value, "研究生") {
		return "硕士"
	}
	return ""
}

// mokaQualifications exposes disclosed structured requirements to the existing
// deterministic matcher while keeping unknown fields and preferences separate.
func mokaQualifications(detail map[string]any, description string) (required, preferred, evidence, reason string) {
	required, preferred = mokaSplitQualifications(description)
	evidenceLines := []string{}
	experience := 0
	if raw, exists := detail["minExperience"]; exists && raw != nil {
		n, err := strconv.Atoi(text(raw))
		if err != nil || n < 0 || n > 99 {
			return "", "", "", "官方最低工作经验字段无法确认，需人工复核"
		}
		experience = n
		evidenceLines = append(evidenceLines, "官方最低经验字段（年）："+text(raw))
	}
	education := PlainText(text(detail["education"]))
	if degree := mokaDegree(education); degree != "" {
		required += "\n官方学历字段：" + degree + "及以上"
	}
	for _, field := range mokaPublicFields(detail) {
		evidenceLines = append(evidenceLines, "官方字段 "+field.name+"："+field.value)
		if mokaPreferred(field.name) {
			preferred += "\n官方优先字段 " + field.name + "：" + field.value
			continue
		}
		hardValue, preferredValue := mokaSplitQualifications(field.value)
		if preferredValue != "" {
			preferred += "\n官方优先字段 " + field.name + "：" + preferredValue
		}
		if hardValue == "" {
			continue
		}
		if strings.Contains(field.name, "学历") || strings.Contains(field.name, "学位") {
			if degree := mokaDegree(hardValue); degree != "" {
				required += "\n官方学历要求：" + degree + "及以上"
			}
		}
		if !strings.Contains(field.name, "工作经验") {
			continue
		}
		for _, clause := range strings.Split(hardValue, "\n") {
			switch strings.ToLower(strings.TrimSpace(clause)) {
			case "不限", "经验不限", "工作经验不限", "无需工作经验", "无工作经验", "无经验", "应届生", "应届毕业生", "no experience", "no experience required":
				continue
			}
			match := mokaFieldExperience.FindStringSubmatch(clause)
			if len(match) != 2 {
				return "", "", "", "官方工作经验要求无法确认，需人工复核"
			}
			n, valid := mokaNumber(match[1])
			if match[1] == "0" {
				n, valid = 0, true
			}
			if !valid {
				return "", "", "", "官方工作经验要求无法确认，需人工复核"
			}
			experience = max(experience, n)
		}
	}
	for _, clause := range mokaExperienceClause.Split(required, -1) {
		for _, match := range mokaEmploymentExperience.FindAllStringSubmatch(clause, -1) {
			if n, valid := mokaNumber(match[1]); valid {
				experience = max(experience, n)
			}
		}
		if match := mokaEnglishWorkExperience.FindStringSubmatch(clause); len(match) == 2 {
			n, err := strconv.Atoi(match[1])
			if err != nil {
				return "", "", "", "官方工作经验要求无法确认，需人工复核"
			}
			experience = max(experience, n)
		}
	}
	if experience > 1 {
		return "", "", "", "官方硬条件要求超过一年工作经验，不属于本版范围"
	}
	if experience > 0 {
		required += fmt.Sprintf("\n官方工作经验要求：要求 %d 年相关工作经验", experience)
	}
	return required, strings.TrimSpace(preferred), strings.Join(evidenceLines, "\n"), ""
}
