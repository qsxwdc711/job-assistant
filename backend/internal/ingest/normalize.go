// Package ingest reads verified public recruitment endpoints without account credentials.
package ingest

import (
	"html"
	"regexp"
	"sort"
	"strings"

	"jobassistant/backend/internal/catalog"
)

var (
	unsafeBlocks    = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)\s*>`)
	lineTags        = regexp.MustCompile(`(?i)<\s*(br\s*/?|/(p|div|li|h[1-6]))\s*>`)
	htmlTags        = regexp.MustCompile(`<[^>]*>`)
	horizontalSpace = regexp.MustCompile(`[\t\p{Zs}]+`)
	placeSeparators = regexp.MustCompile(`[/、,，;；\s]+`)
	majorLanguage   = regexp.MustCompile(`相关专业|专业背景|不限专业|专业不限|专业要求|专业[：:]|(?:计算机|软件工程|通信|电子|数学|信息技术|人工智能)[^。；\n]{0,80}专业`)
)

// PlainText removes presentation markup; clients render the result as text, never HTML.
func PlainText(value string) string {
	value = unsafeBlocks.ReplaceAllString(value, "")
	value = lineTags.ReplaceAllString(value, "\n")
	value = html.UnescapeString(htmlTags.ReplaceAllString(value, ""))
	lines := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r", ""), "\n") {
		line = strings.TrimSpace(horizontalSpace.ReplaceAllString(line, " "))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// ParseMajor retains uncertain related-major language instead of asserting eligibility.
func ParseMajor(requirements string) catalog.MajorRequirement {
	text := PlainText(requirements)
	result := catalog.MajorRequirement{Kind: "unknown", Tags: []string{}, Evidence: ""}
	for _, line := range strings.Split(text, "\n") {
		if !majorLanguage.MatchString(line) {
			continue
		}
		result.Evidence = line
		if strings.Contains(line, "专业不限") || strings.Contains(line, "不限专业") {
			result.Kind = "unrestricted"
			return result
		}
		terms := []struct{ word, tag string }{{"计算机", "computer_science"}, {"软件工程", "software_engineering"}, {"通信", "communications"}, {"电子", "electronics"}, {"数学", "mathematics"}, {"信息技术", "information_technology"}, {"人工智能", "artificial_intelligence"}}
		for _, term := range terms {
			if strings.Contains(line, term.word) {
				result.Tags = append(result.Tags, term.tag)
			}
		}
		if len(result.Tags) > 0 {
			result.Kind = "explicit"
			if strings.Contains(line, "相关") || strings.Contains(line, "类") {
				result.Kind = "related"
			}
			if strings.Contains(line, "优先") {
				result.Kind = "preferred"
			}
			sort.Strings(result.Tags)
			return result
		}
	}
	return result
}

// Direction classifies the actual role and duties, not whether a nontechnical job accepts a major.
func Direction(title, responsibilities string) string {
	classify := func(text string) string {
		text = strings.ToLower(text)
		groups := []struct {
			direction string
			terms     []string
		}{
			{"security", []string{"安全", "security", "隐私"}},
			{"mobile", []string{"移动端", "android", "ios", "客户端"}},
			{"frontend", []string{"前端", "frontend"}},
			{"test", []string{"测试", "质量保障", "qa工程"}},
			{"embedded", []string{"嵌入式", "驱动开发", "fpga", "固件", "技术研究-机器人"}},
			{"operations_sre", []string{"sre", "运维", "技术运营", "ai infra", "训推系统", "基础设施"}},
			{"algorithm_ai", []string{"算法", "人工智能", "ai模型", "ai应用", "agent", "机器学习", "深度学习", "建模仿真"}},
			{"data", []string{"数据分析", "数据工程", "数据系统", "数据科学", "大数据"}},
			{"backend", []string{"后台", "后端", "软件开发", "数据库", "编译器", "操作系统", "应用开发", "程序开发", "软件研发", "通用软件", "数字金融", "软件工程师", "基础架构", "高性能计算", "渲染引擎", "游戏引擎", "运营开发", "全栈", "云服务开发"}},
		}
		for _, group := range groups {
			for _, term := range group.terms {
				if strings.Contains(text, term) {
					return group.direction
				}
			}
		}
		return "unknown"
	}
	if direction := classify(title); direction != "unknown" {
		return direction
	}
	for _, term := range []string{"宏观经济", "供应链管理", "市场营销", "客户经理", "人力资源", "行政助理", "财务管理", "投资研究"} {
		if strings.Contains(title, term) {
			return "unknown"
		}
	}
	for _, term := range []string{"程序开发", "编写代码", "编码实现", "软件系统", "服务器开发", "FPGA", "操作系统", "Linux", "数据库", "算法实现", "应用系统开发"} {
		if strings.Contains(responsibilities, term) {
			return classify(responsibilities)
		}
	}
	return "unknown"
}

var cityProvinces = map[string]string{
	"北京": "北京市", "上海": "上海市", "天津": "天津市", "重庆": "重庆市",
	"深圳": "广东省", "广州": "广东省", "东莞": "广东省", "佛山": "广东省", "惠州": "广东省", "珠海": "广东省",
	"杭州": "浙江省", "宁波": "浙江省", "南京": "江苏省", "苏州": "江苏省", "无锡": "江苏省",
	"成都": "四川省", "武汉": "湖北省", "长沙": "湖南省", "西安": "陕西省", "合肥": "安徽省", "济南": "山东省", "青岛": "山东省",
	"郑州": "河南省", "厦门": "福建省", "福州": "福建省", "大连": "辽宁省", "沈阳": "辽宁省", "长春": "吉林省", "哈尔滨": "黑龙江省",
	"昆明": "云南省", "贵阳": "贵州省", "南宁": "广西壮族自治区", "南昌": "江西省", "太原": "山西省", "石家庄": "河北省",
	"呼和浩特": "内蒙古自治区", "包头": "内蒙古自治区", "鄂尔多斯": "内蒙古自治区", "呼伦贝尔": "内蒙古自治区",
}

// Locations normalizes known prefectures with internal keys and preserves unclassified originals.
func Locations(value string) []catalog.Location {
	result := []catalog.Location{}
	seen := map[string]bool{}
	for _, raw := range placeSeparators.Split(strings.TrimSpace(value), -1) {
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		name := strings.TrimSuffix(strings.TrimSuffix(raw, "总部"), "市")
		location := catalog.Location{Kind: "unknown", Raw: raw, Code: "raw:" + raw, Country: "unknown", City: raw, Province: "unknown"}
		if province, ok := cityProvinces[name]; ok {
			location.Kind = "domestic"
			location.Country = "中国"
			location.Province = province
			location.City = name + "市"
			location.Code = "CN:" + province + ":" + location.City
		}
		if raw == "海外" || raw == "境外" {
			location.Kind = "overseas"
			location.City = "未明确"
			location.Code = "overseas"
		}
		result = append(result, location)
	}
	return result
}
