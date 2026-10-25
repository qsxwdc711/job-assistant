package ingest

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"jobassistant/backend/internal/catalog"
)

var (
	workdayInternTitle = regexp.MustCompile(`(?i)\bintern(?:ship)?\b`)
	workdayPreferred   = regexp.MustCompile(`(?i)\b(?:preferred|preferable|plus|bonus|advantage|nice[- ]to[- ]have)\b`)
	workdayStudent     = regexp.MustCompile(`(?i)\b(?:pursuing|currently enrolled|enrolled in|undergraduate student|graduate student)\b|在读|在校`)
	workdayBachelor    = regexp.MustCompile(`(?i)\b(?:b\.?s\.?|b\.?sc\.?|bachelor(?:'s|s)?|undergraduate)\b`)
	workdayMaster      = regexp.MustCompile(`(?i)\b(?:m\.?s\.?|m\.?sc\.?|master(?:'s|s)?)\b`)
	workdayDoctorate   = regexp.MustCompile(`(?i)\b(?:ph\.?d\.?|doctorate|doctoral)\b`)
	workdayYears       = regexp.MustCompile(`(?i)\b(?:(at least|minimum(?: of)?|more than|over)\s+)?([0-9]+|zero|one|two|three|four|five|six|seven|eight|nine|ten)(?:\s*(?:[-–—]|to)\s*([0-9]+))?\s*\+?\s*years?'?(?:\s+of)?(?:[ -]+[a-z+#/]+){0,6}[ -]+experience\b`)
	workdayGraduate    = regexp.MustCompile(`(?i)\b(?:new|recent) graduates?\b.{0,40}\b(?:welcome|accepted|considered|eligible)\b|\bno (?:prior )?(?:work |professional )?experience (?:is )?(?:required|necessary)\b`)
	workdayMonths      = regexp.MustCompile(`(?i)\b(?:at least|minimum(?: of)?|no less than)\s+([0-9]{1,2})\s+(?:consecutive\s+)?months?\b`)
	workdayDays        = regexp.MustCompile(`(?i)\b(?:at least|minimum(?: of)?|no less than)\s+([1-9])\s+days?\s+(?:per|a|each)\s+week\b`)
	workdayMajorTerms  = []struct {
		pattern *regexp.Regexp
		tag     string
	}{
		{regexp.MustCompile(`(?i)\bcomputer science\b|\bcs\b`), "computer_science"},
		{regexp.MustCompile(`(?i)\bsoftware engineering\b|\bse\b`), "software_engineering"},
		{regexp.MustCompile(`(?i)\belectrical engineering\b|\belectronic(?:s)?\b|\bee\b`), "electronics"},
		{regexp.MustCompile(`(?i)\bcomputer engineering\b|\bce\b`), "computer_science"},
		{regexp.MustCompile(`(?i)\b(?:applied )?mathematics\b|\bmath\b`), "mathematics"},
		{regexp.MustCompile(`(?i)\bartificial intelligence\b`), "artificial_intelligence"},
	}
)

func workdayDirection(title, duties string) string {
	lower, body := strings.ToLower(title), strings.ToLower(duties)
	for _, term := range []string{"international", "sales", "marketing", "finance", "human resources", "recruiter", "manager", "director", "head of", "technical writer", "physical design", "circuit design", "package design", "mechanical", "thermal", "board design", "dft intern"} {
		if strings.Contains(lower, term) {
			return "unknown"
		}
	}
	if strings.Contains(lower, "security") {
		return "security"
	}
	if strings.Contains(lower, "software") && (strings.Contains(lower, "test") || strings.Contains(lower, "quality")) {
		return "test"
	}
	for _, term := range []string{"frontend", "front-end", "web developer"} {
		if strings.Contains(lower, term) && !strings.Contains(lower, "hardware") {
			return "frontend"
		}
	}
	for _, term := range []string{"android", "ios developer", "mobile software"} {
		if strings.Contains(lower, term) {
			return "mobile"
		}
	}
	for _, term := range []string{"firmware", "embedded", "driver", "fpga"} {
		if strings.Contains(lower, term) {
			return "embedded"
		}
	}
	for _, term := range []string{"site reliability", "sre", "devops", "infrastructure engineer"} {
		if strings.Contains(lower, term) {
			return "operations_sre"
		}
	}
	for _, term := range []string{"data scientist", "data engineer", "data science"} {
		if strings.Contains(lower, term) {
			return "data"
		}
	}
	computerDuties := strings.Contains(lower, "software engineer") || strings.Contains(lower, "software developer") || strings.Contains(lower, "software development") || strings.Contains(lower, "system software") || strings.Contains(lower, "compiler")
	for _, term := range []string{"software", "programming", "c++", "c/c++", "python", "cuda", "pytorch", "algorithm", "code", "neural", "machine learning", "deep learning"} {
		computerDuties = computerDuties || strings.Contains(body, term)
	}
	if computerDuties {
		for _, term := range []string{"deep learning", "machine learning", " ai ", "ai developer", "algorithm", "robotics", "autonomous", "research", "inference"} {
			if strings.Contains(lower, term) {
				return "algorithm_ai"
			}
		}
		for _, term := range []string{"software", "compiler", "developer technology", "gpu computing", "high-performance computing", "high performance computing", "gpu architecture"} {
			if strings.Contains(lower, term) {
				return "backend"
			}
		}
		if (strings.Contains(lower, "verification") || strings.Contains(lower, "asic")) && (strings.Contains(body, "systemverilog") || strings.Contains(body, "testbench") || strings.Contains(body, "rtl")) {
			return "embedded"
		}
	}
	return Direction(title, duties)
}

func workdaySections(description string) (string, string, string) {
	var duties, hard, preferred []string
	mode := "duties"
	for _, line := range strings.Split(description, "\n") {
		heading := strings.Trim(strings.ToLower(strings.ReplaceAll(line, "’", "'")), " \t:.")
		switch heading {
		case "what you'll be doing", "what you will be doing", "what you'll do", "responsibilities", "job responsibilities":
			mode = "duties"
			continue
		case "what we need to see", "what you'll need", "what you will need", "minimum qualifications", "required qualifications", "qualifications", "requirements", "who you are":
			mode = "hard"
			continue
		case "ways to stand out from the crowd", "preferred qualifications", "preferred skills", "nice to have", "bonus qualifications":
			mode = "preferred"
			continue
		}
		if strings.HasPrefix(heading, "we are an equal opportunity") || strings.HasPrefix(heading, "nvidia is committed to") || strings.HasPrefix(heading, "with highly competitive salaries") {
			mode = "footer"
		}
		switch mode {
		case "duties":
			duties = append(duties, line)
		case "hard":
			hard = append(hard, line)
		case "preferred":
			preferred = append(preferred, line)
		}
	}
	return strings.TrimSpace(strings.Join(duties, "\n")), strings.TrimSpace(strings.Join(hard, "\n")), strings.TrimSpace(strings.Join(preferred, "\n"))
}

// workdayHardLine keeps a hard bachelor before a semicolon separate from a
// preferred master. A wholly preferred clause never supplies a hard minimum.
func workdayHardLine(line string) string {
	var hard []string
	for _, part := range strings.Split(line, ";") {
		if workdayPreferred.MatchString(part) {
			continue
		}
		hard = append(hard, part)
	}
	return strings.Join(hard, ";")
}

func workdayDegreeFloor(hard string) int {
	minimum := 0
	for _, line := range strings.Split(hard, "\n") {
		line = workdayHardLine(line)
		level := 0
		switch {
		case workdayBachelor.MatchString(line):
			level = 2
		case workdayMaster.MatchString(line):
			level = 3
		case workdayDoctorate.MatchString(line):
			level = 4
		}
		minimum = max(minimum, level)
	}
	return minimum
}

func workdayMajor(hard, preferred string) catalog.MajorRequirement {
	for _, block := range []struct {
		value string
		bonus bool
	}{{hard, false}, {preferred, true}} {
		for _, original := range strings.Split(block.value, "\n") {
			line := original
			if !block.bonus {
				line = workdayHardLine(line)
			}
			var tags []string
			for _, term := range workdayMajorTerms {
				if term.pattern.MatchString(line) && !slices.Contains(tags, term.tag) {
					tags = append(tags, term.tag)
				}
			}
			if len(tags) == 0 {
				continue
			}
			kind := "explicit"
			if strings.Contains(strings.ToLower(line), "related") || strings.Contains(strings.ToLower(line), "equivalent") {
				kind = "related"
			}
			if block.bonus {
				kind = "preferred"
			}
			sort.Strings(tags)
			return catalog.MajorRequirement{Kind: kind, Tags: tags, Evidence: original}
		}
	}
	return catalog.MajorRequirement{Kind: "unknown", Tags: []string{}}
}

func workdayCommitments(hard string) (months, days *int, err error) {
	for _, line := range strings.Split(hard, "\n") {
		line = workdayHardLine(line)
		for _, rule := range []struct {
			pattern *regexp.Regexp
			limit   int
			target  **int
		}{{workdayMonths, 60, &months}, {workdayDays, 7, &days}} {
			for _, match := range rule.pattern.FindAllStringSubmatch(line, -1) {
				n, parseErr := strconv.Atoi(match[1])
				if parseErr != nil || n < 1 || n > rule.limit {
					return nil, nil, ErrSchema
				}
				if *rule.target == nil || n > **rule.target {
					value := n
					*rule.target = &value
				}
			}
		}
	}
	return months, days, nil
}

func workdayExperience(hard string) (minimum int, explicit bool, workMinimum int) {
	for _, line := range strings.Split(hard, "\n") {
		for _, clause := range strings.Split(workdayHardLine(line), ";") {
			lower := strings.ToLower(clause)
			professional := strings.Contains(lower, "work experience") || strings.Contains(lower, "professional") || strings.Contains(lower, "industry experience") || strings.Contains(lower, "working experience")
			if !professional && (strings.Contains(lower, "project experience") || strings.Contains(lower, "academic experience") || strings.Contains(lower, "coursework")) {
				continue
			}
			for _, match := range workdayYears.FindAllStringSubmatch(clause, -1) {
				n, err := strconv.Atoi(match[2])
				if err != nil {
					n = map[string]int{"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10}[strings.ToLower(match[2])]
				}
				if strings.EqualFold(match[1], "more than") || strings.EqualFold(match[1], "over") {
					n++
				}
				minimum, explicit = max(minimum, n), true
				if professional {
					workMinimum = max(workMinimum, n)
				}
			}
		}
	}
	return minimum, explicit, workMinimum
}

func workdayLocations(info map[string]any) ([]catalog.Location, error) {
	country := text(object(info["country"])["descriptor"])
	if country == "" {
		return nil, ErrSchema
	}
	primary := text(info["location"])
	if primary == "" {
		return nil, ErrSchema
	}
	values := []string{primary}
	if raw, exists := info["additionalLocations"]; exists {
		additional, ok := raw.([]any)
		if !ok {
			return nil, ErrSchema
		}
		for _, raw := range additional {
			value, ok := raw.(string)
			if !ok || strings.TrimSpace(value) == "" {
				return nil, ErrSchema
			}
			values = append(values, value)
		}
	}
	known := map[string]string{"China, Shanghai": "上海", "China, Beijing": "北京", "China, Shenzhen": "深圳", "China, Chengdu": "成都", "China, Wuhan": "武汉", "China, Hangzhou": "杭州", "China, Suzhou": "苏州", "China, Nanjing": "南京", "China, Guangzhou": "广州"}
	seen := map[string]bool{}
	var result []catalog.Location
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		if city, ok := known[value]; ok {
			loc := Locations(city)[0]
			loc.Raw = value
			result = append(result, loc)
			continue
		}
		parts := strings.SplitN(value, ",", 2)
		place := catalog.Location{Code: "workday:" + value, Kind: "unknown", Country: "unknown", Province: "unknown", City: value, Raw: value}
		if len(parts) == 2 {
			place.Country, place.City = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			if place.Country == "China" {
				place.Country = "中国"
			} else {
				place.Kind = "overseas"
			}
		}
		result = append(result, place)
	}
	return result, nil
}

func workdayDocument(site workdaySite, kind workdayType, detail map[string]any) (catalog.Document, string, error) {
	info := object(detail["jobPostingInfo"])
	id, title := text(info["jobReqId"]), PlainText(text(info["title"]))
	description := PlainText(text(info["jobDescription"]))
	if !workdayReqID.MatchString(id) || title == "" || description == "" || info["posted"] != true || info["canApply"] != true {
		return catalog.Document{}, "", ErrSchema
	}
	if !slices.Contains([]string{"internship", "campus", "early_career"}, kind.kind) {
		return catalog.Document{}, "", ErrSchema
	}
	if (kind.kind != "internship" && workdayInternTitle.MatchString(title)) || (kind.kind == "internship" && strings.Contains(strings.ToLower(title), "new college grad")) {
		return catalog.Document{}, "官方原生招聘类型与实习标题冲突，保留待人工核验", nil
	}
	if text(object(info["country"])["descriptor"]) != "China" {
		return catalog.Document{}, "本来源仅核验中国岗位，当前详情国家不在已验证范围内", nil
	}
	duties, hard, preferred := workdaySections(description)
	if hard == "" {
		return catalog.Document{}, "官方岗位资格分段未能可靠识别，保留原文待人工核验", nil
	}
	if duties == "" {
		duties = description
	}
	direction := workdayDirection(title, duties)
	if direction == "unknown" {
		return catalog.Document{}, "实际职责未确认属于计算机技术岗位，保留待人工核验", nil
	}
	years, explicit, workYears := workdayExperience(hard)
	if years > 1 {
		return catalog.Document{}, "官方硬性经验要求超过一年，不属于当前求职范围，保留人工核验", nil
	}
	if kind.kind == "early_career" && !explicit && !workdayGraduate.MatchString(hard) {
		return catalog.Document{}, "社会招聘未明确接受应届或最多一年经验，保留待人工核验", nil
	}
	locations, err := workdayLocations(info)
	if err != nil {
		return catalog.Document{}, "", err
	}
	china := false
	for _, location := range locations {
		china = china || location.Country == "中国"
	}
	if !china {
		return catalog.Document{}, "详情未披露可核实的中国工作地点，保留待人工核验", nil
	}
	student, education := "unknown", "unknown"
	var months, days *int
	if kind.kind == "internship" {
		for _, line := range strings.Split(hard, "\n") {
			if workdayStudent.MatchString(workdayHardLine(line)) {
				student = "yes"
				break
			}
		}
		months, days, err = workdayCommitments(hard)
		if err != nil {
			return catalog.Document{}, "", err
		}
	}
	requirements := hard
	switch workdayDegreeFloor(hard) {
	case 2:
		education = "bachelor_or_above"
		requirements += "\n官方学历原文见上方；最低学历：本科及以上"
	case 3:
		requirements += "\n官方学历原文见上方；最低学历：硕士及以上"
	case 4:
		requirements += "\n官方学历原文见上方；最低学历：博士及以上"
	}
	if workYears > 0 {
		requirements += fmt.Sprintf("\n官方工作经验原文见上方；至少%d年工作经验", workYears)
	}
	link := text(info["externalUrl"])
	organization := PlainText(text(object(detail["hiringOrganization"])["name"]))
	if organization == "" || !strings.Contains(strings.ToLower(organization), "nvidia") {
		return catalog.Document{}, "", ErrSchema
	}
	evidence := fmt.Sprintf("企业官网 https://www.nvidia.com/en-us/about-nvidia/careers/ 直接链接 https://jobs.nvidia.com/careers ，公开配置指向本 Workday 官方招聘租户。\n原生过滤类型=%s；workerSubType=%s；locationHierarchy1(China)=%s；posted=true；canApply=true。\n不以 Full time 或 intern 子串代替原生招聘类型，不由标题中的年份推定固定毕业日期窗口或截止日期。\n官方地点=%s；附加地点=%s。\n完整官方岗位原文：\n%s", kind.name, kind.id, site.china, text(info["location"]), joinedStrings(info["additionalLocations"], ""), description)
	variant := catalog.Variant{Key: id, Name: title, Direction: direction, Responsibilities: duties, Requirements: requirements, Preferred: preferred, Major: workdayMajor(hard, preferred), Locations: locations, Departments: []catalog.DepartmentGroup{}}
	return catalog.Document{ExternalID: id, CompanyCode: site.company, SourceCode: site.source, CampaignKey: "workday:" + site.site, CampaignName: "中国官方校招、实习与明确低经验招聘", Title: title, RecruitmentType: kind.kind, Publisher: organization, PublicStatus: "open", StatusEvidence: "完整中国/原生类型筛选列表与同 ID、同标题的官方详情一致，posted=true且canApply=true", WrittenTest: "unknown", SourceURL: link, ApplyURL: link, ApplyLevel: "job", LoginRequired: true, DeadlineTimezone: "unknown", GraduationRules: []catalog.GraduationRule{}, StudentRequired: student, MinimumEducation: education, MinimumInternshipMonths: months, DaysPerWeek: days, CampaignEvidence: evidence, Variants: []catalog.Variant{variant}}, "", nil
}
