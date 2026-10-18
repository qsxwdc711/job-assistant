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
	zjditSocialOnly = regexp.MustCompile(`(?m)(^|[，,。；;\n])([ \t]*(?:[0-9]+[.、][ \t]*)?)社招(?:人员)?(?:需|须|要求)[^，,。；;\n]*`)
	zjditClauses    = regexp.MustCompile(`[，,。；;\n]`)
	zjditYears      = regexp.MustCompile(`([一二三四五六七八九十0-9]+)\s*年`)
)

func zjditDirection(title string) string {
	for _, word := range []string{"技术支持", "应用工程师", "产品总监", "负责人", "经理"} {
		if strings.Contains(title, word) {
			return "unknown"
		}
	}
	if direction := Direction(title, ""); direction != "unknown" {
		return direction
	}
	if strings.Contains(title, "并行计算") {
		return "backend"
	}
	if strings.Contains(title, "C++开发") || strings.Contains(title, "引擎研发") || strings.Contains(title, "界面定制引擎") {
		return "backend"
	}
	return "unknown"
}

func zjditYearsValue(value string) (int, bool) {
	if n, err := strconv.Atoi(value); err == nil && n > 0 && n <= 99 {
		return n, true
	}
	n, ok := map[string]int{"一": 1, "二": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9, "十": 10}[value]
	return n, ok
}

func zjditExperience(requirements string) (maximum int) {
	for _, clause := range zjditClauses.Split(requirements, -1) {
		if !strings.Contains(clause, "经验") || strings.Contains(clause, "优先") || strings.Contains(clause, "加分") || strings.Contains(clause, "不限") || strings.Contains(clause, "无需") {
			continue
		}
		for _, match := range zjditYears.FindAllStringSubmatch(clause, -1) {
			n, ok := zjditYearsValue(match[1])
			if !ok {
				return 100
			}
			maximum = max(maximum, n)
		}
	}
	return maximum
}

func zjditDocument(entry map[string]any) (catalog.Document, string, error) {
	labels, err := zjditLabels(entry["labels"])
	if err != nil {
		return catalog.Document{}, "", err
	}
	id, title := text(entry["id"]), PlainText(text(entry["name"]))
	duties, rawRequirements := PlainText(text(entry["responsibility"])), PlainText(text(entry["requirement"]))
	if duties == "" || rawRequirements == "" || PlainText(text(entry["qualification"])) == "" || PlainText(text(entry["city"])) == "" {
		return catalog.Document{}, "", ErrSchema
	}
	kind, requirements := "early_career", rawRequirements
	if strings.Contains(title, "实习") {
		kind = "internship"
	} else if slices.Contains(labels, "校招") {
		kind = "campus"
		requirements = strings.TrimSpace(zjditSocialOnly.ReplaceAllString(rawRequirements, "${1}${2}"))
	}
	years := zjditExperience(requirements)
	if years > 1 {
		return catalog.Document{}, "未限定为社招的任职条件要求超过一年经验，暂不发布，保留人工核验", nil
	}
	if kind == "early_career" && years == 0 && !strings.Contains(requirements, "接受应届") && !strings.Contains(requirements, "欢迎应届") && !strings.Contains(requirements, "经验不限") && !strings.Contains(requirements, "无需工作经验") {
		return catalog.Document{}, "仅社招标签且接受应届或低经验的依据不足，保留人工核验", nil
	}
	qualification := PlainText(text(entry["qualification"]))
	for _, degree := range []string{"专科", "本科", "硕士", "博士"} {
		if qualification == degree || qualification == degree+"及以上" {
			requirements += "\n官方学历字段：" + degree + "及以上"
			break
		}
	}
	link := "https://zp.zjdit.com/Position/details.html?id=" + id
	evidence := fmt.Sprintf("企业官网 https://www.zjdit.com/ 直接链接招聘站 https://zp.zjdit.com/\n技术类别 class_id=1；当前列表标签：%s；公开发布日期：%s（非投递截止）\n本条按%s保存；明确社招专属条件不作为校园要求。官方完整任职条件：\n%s\n未由通用 FAQ 推断本岗位笔试、毕业窗口或截止。", strings.Join(labels, "/"), text(entry["release_time"]), kind, rawRequirements)
	variant := catalog.Variant{Key: id, Name: title, Direction: zjditDirection(title), Responsibilities: duties, Requirements: requirements, Major: ParseMajor(requirements), Locations: Locations(PlainText(text(entry["city"]))), Departments: []catalog.DepartmentGroup{}}
	doc := catalog.Document{
		ExternalID: id, CompanyCode: "zjdit", SourceCode: zjditSource, CampaignKey: "zjdit:technical", CampaignName: "官方技术招聘（以岗位标签为准）",
		Title: title, RecruitmentType: kind, Publisher: "四川中久数创科技有限公司", PublicStatus: "open", StatusEvidence: "当前公开技术列表在列、listing_delisting=1/is_del=0；同 ID 主详情与列表一致且显示登录投递",
		WrittenTest: "unknown", SourceURL: link, ApplyURL: link, ApplyLevel: "job", LoginRequired: true, DeadlineTimezone: "unknown", GraduationRules: []catalog.GraduationRule{},
		StudentRequired: "unknown", MinimumEducation: "unknown", CampaignEvidence: evidence, Variants: []catalog.Variant{variant},
	}
	return doc, "", nil
}
