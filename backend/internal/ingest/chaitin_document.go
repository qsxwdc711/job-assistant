package ingest

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"jobassistant/backend/internal/catalog"
)

var (
	chaitinAI      = regexp.MustCompile(`(?i)\bai\b|算法|大模型`)
	chaitinClauses = regexp.MustCompile(`[，,。；;\n]`)
	chaitinYears   = regexp.MustCompile(`([一二两三四五六七八九十0-9]+)(?:\s*[-~–—至]\s*[一二两三四五六七八九十0-9]+)?\s*年(?:及以上|以上)?[^，,。；;\n]{0,20}经验`)
	chaitinDays    = regexp.MustCompile(`每周(?:至少|不少于)?(?:到岗|出勤|实习)?\s*([一二三四五六七1-7])\s*天`)
	chaitinMonths  = regexp.MustCompile(`(?:至少|不少于|不低于)\s*([一二两三四五六七八九十0-9]+)\s*个?月|([一二两三四五六七八九十0-9]+)\s*个?月(?:及以上|以上)`)
	chaitinCohort  = regexp.MustCompile(`20[0-9]{2}届`)
	chaitinMaster  = regexp.MustCompile(`硕士\s*[/／]\s*博士生|在校博士生或优秀硕士生`)
)

func chaitinDirection(job map[string]any) string {
	if !slices.Contains([]string{"研发类", "安全服务类", "技术服务类"}, text(job["job_category_name"])) {
		return "unknown"
	}
	title, description := text(job["title"]), strings.ToLower(text(job["description"]))
	for _, excluded := range []string{"经理", "总监", "售前", "销售", "产品运营"} {
		if strings.Contains(title, excluded) {
			return "unknown"
		}
	}
	if strings.Contains(title, "技术支持") {
		if strings.Contains(description, "linux") && (strings.Contains(description, "运维") || strings.Contains(description, "部署") || strings.Contains(description, "故障")) {
			return "operations_sre"
		}
		return "unknown"
	}
	lower := strings.ToLower(title)
	if strings.Contains(lower, "hids") || strings.Contains(lower, "dast") || strings.Contains(lower, "security") || strings.Contains(lower, "安全") {
		return "security"
	}
	if chaitinAI.MatchString(title) {
		return "algorithm_ai"
	}
	if direction := Direction(title, ""); direction != "unknown" {
		return direction
	}
	for _, language := range []string{"golang", "java", "c++", "python", "端开发", "引擎开发"} {
		if strings.Contains(lower, language) {
			return "backend"
		}
	}
	return "unknown"
}

func chaitinNumber(value string) (int, bool) {
	if n, err := strconv.Atoi(value); err == nil && n >= 0 && n <= 99 {
		return n, true
	}
	n, ok := map[string]int{"一": 1, "二": 2, "两": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9, "十": 10, "十一": 11, "十二": 12}[value]
	return n, ok
}

// chaitinSections keeps preferred qualifications outside deterministic hard checks.
// The complete original description remains in CampaignEvidence in all cases.
func chaitinSections(description string) (duties, requirements, preferred string) {
	lines := strings.Split(description, "\n")
	start, bonus := -1, len(lines)
	for i, line := range lines {
		heading := strings.Trim(line, " \t#*【】[]：:")
		if slices.Contains([]string{"任职要求", "岗位要求", "任职资格", "基本要求", "资格要求"}, heading) && start < 0 {
			start = i
		}
		if slices.Contains([]string{"加分项", "加分条件", "优先条件", "优先考虑"}, heading) && bonus == len(lines) {
			bonus = i
		}
	}
	if start >= bonus {
		return description, description, "" // Ambiguous order must not discard later hard requirements.
	}
	if bonus < len(lines) {
		preferred = strings.Join(lines[bonus+1:], "\n")
	}
	if start < 0 {
		requirements = strings.Join(lines[:bonus], "\n")
		duties = requirements
	} else {
		duties = strings.Join(lines[:start], "\n")
		requirements = strings.Join(lines[start+1:bonus], "\n")
		if duties == "" {
			duties = description
		}
	}
	return strings.TrimSpace(duties), strings.TrimSpace(requirements), strings.TrimSpace(preferred)
}

func chaitinExperience(requirements string) (minimum int, explicit bool) {
	for _, clause := range chaitinClauses.Split(requirements, -1) {
		if strings.Contains(clause, "优先") || strings.Contains(clause, "加分") || strings.Contains(clause, "不限") || strings.Contains(clause, "无需") {
			continue
		}
		for _, match := range chaitinYears.FindAllStringSubmatch(clause, -1) {
			n, ok := chaitinNumber(match[1])
			if !ok {
				return 100, true
			}
			if strings.Contains(clause, "超过") {
				n++
			}
			minimum, explicit = max(minimum, n), true
		}
	}
	return minimum, explicit
}

func chaitinDocument(job map[string]any, brand chaitinBrand) (catalog.Document, string, error) {
	direction := chaitinDirection(job)
	if direction == "unknown" {
		return catalog.Document{}, "官方类别或实际职责未确认属于计算机技术岗位，保留待人工核验", nil
	}
	id, title := text(job["job_id"]), PlainText(text(job["title"]))
	description := PlainText(text(job["description"]))
	duties, requirements, preferred := chaitinSections(description)
	if id == "" || title == "" || duties == "" || requirements == "" {
		return catalog.Document{}, "", ErrSchema
	}
	kind := "early_career"
	if text(job["work_type"]) == "internship" {
		kind = "internship"
	} else if strings.Contains(title, "实习") {
		return catalog.Document{}, "实习标题与全职字段不一致，保留待人工核验", nil
	} else if text(job["recruitment_type"]) == "campus" {
		kind = "campus"
	} else if chaitinCohort.MatchString(title) || strings.Contains(title, "校招") {
		return catalog.Document{}, "校招标题与社招字段不一致，保留待人工核验", nil
	}
	years, explicit := chaitinExperience(requirements)
	if years > 1 {
		return catalog.Document{}, "任职要求存在超过一年硬性经验门槛，不属于本版范围，保留人工核验", nil
	}
	if kind == "early_career" && !explicit && !strings.Contains(requirements, "接受应届") && !strings.Contains(requirements, "欢迎应届") && !strings.Contains(requirements, "应届毕业生") && !strings.Contains(requirements, "经验不限") && !strings.Contains(requirements, "无需工作经验") {
		return catalog.Document{}, "社招接受应届或低经验的依据不足，保留待人工核验", nil
	}
	student, education := "unknown", "unknown"
	var days, months *int
	for _, clause := range chaitinClauses.Split(requirements, -1) {
		if strings.Contains(clause, "优先") || strings.Contains(clause, "加分") {
			continue
		}
		if strings.Contains(clause, "本科及以上") || strings.Contains(clause, "本科以上") || strings.Contains(clause, "本科学历") || strings.Contains(clause, "在校本科生及以上") {
			education = "bachelor_or_above"
		}
		if kind != "internship" {
			continue
		}
		if strings.Contains(clause, "在校") || strings.Contains(clause, "在读") {
			student = "yes"
		}
		if match := chaitinDays.FindStringSubmatch(clause); len(match) > 1 {
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
	if strings.Contains(requirements, "硕士及以上") || strings.Contains(requirements, "博士及以上") || strings.Contains(requirements, "研究生及以上") {
		education = "unknown" // Preserve the higher floor in the per-direction requirements.
	}
	for _, clause := range chaitinClauses.Split(requirements, -1) {
		if !strings.Contains(clause, "优先") && chaitinMaster.MatchString(clause) {
			requirements += "\n官方学历原文要求硕士/博士生；最低学历：硕士及以上"
			education = "unknown"
			break
		}
	}
	if kind == "early_career" {
		employment := 0
		for _, clause := range chaitinClauses.Split(requirements, -1) {
			if strings.Contains(clause, "工作经验") || strings.Contains(clause, "从业经验") {
				n, _ := chaitinExperience(clause)
				employment = max(employment, n)
			}
		}
		if employment > 0 {
			requirements += fmt.Sprintf("\n官方经验要求：至少%d年工作经验（原文见上方及批次依据）", employment)
		}
	}
	link := "https://join.chaitin.cn" + chaitinPagePath + "/jobs/" + id
	evidence := fmt.Sprintf(
		"企业官网 https://www.chaitin.cn/ 直接链接当前公开招聘站。"+
			"\n官方类别=%s；recruitment_type=%s；work_type=%s；岗位标题=%s。"+
			"\n未据公开发布/更新时间或届数编造截止和毕业日期窗口。"+
			"\n官方投递说明：由本人发邮件至 %s；主题模板=%s；本条追踪码=%s；按官方岗位页面说明附 PDF 简历。未代发邮件。"+
			"\n完整官方岗位原文：\n%s",
		text(job["job_category_name"]), text(job["recruitment_type"]), text(job["work_type"]), title,
		brand.Email, brand.Subject, text(job["tracking_code"]), description,
	)
	variant := catalog.Variant{Key: id, Name: title, Direction: direction, Responsibilities: duties, Requirements: requirements, Preferred: preferred, Major: ParseMajor(requirements), Locations: Locations(text(job["location"])), Departments: []catalog.DepartmentGroup{}}
	return catalog.Document{ExternalID: id, CompanyCode: "chaitin", SourceCode: chaitinSource, CampaignKey: "chaitin:default", CampaignName: "官方校招、实习与低经验招聘", Title: title, RecruitmentType: kind, Publisher: "长亭科技；具体招聘主体以官方岗位说明为准", PublicStatus: "open", StatusEvidence: "当前官方公开完整列表与同 ID JSON 详情一致，岗位页面 ID 及品牌投递配置一致；未据发布时间推断截止", WrittenTest: "unknown", SourceURL: link, ApplyURL: link, ApplyLevel: "job", LoginRequired: false, DeadlineTimezone: "unknown", GraduationRules: []catalog.GraduationRule{}, StudentRequired: student, MinimumEducation: education, MinimumInternshipMonths: months, DaysPerWeek: days, CampaignEvidence: evidence, Variants: []catalog.Variant{variant}}, "", nil
}
