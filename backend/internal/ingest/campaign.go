package ingest

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jobassistant/backend/internal/catalog"
)

var dateRange = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日\s*[-—至]\s*(\d{4})年(\d{1,2})月(\d{1,2})日`)
var campusYear = regexp.MustCompile(`(\d{4})届应届生招聘对象`)
var configScript = regexp.MustCompile(`(?is)<script\b[^>]*type\s*=\s*["']application/json["'][^>]*>(.*?)</script\s*>`)

func graduationRange(value string) (string, string, bool) {
	m := dateRange.FindStringSubmatch(value)
	if len(m) != 7 {
		return "", "", false
	}
	numbers := make([]int, 6)
	for i := range numbers {
		v, err := strconv.Atoi(m[i+1])
		if err != nil {
			return "", "", false
		}
		numbers[i] = v
	}
	from := fmt.Sprintf("%04d-%02d-%02d", numbers[0], numbers[1], numbers[2])
	through := fmt.Sprintf("%04d-%02d-%02d", numbers[3], numbers[4], numbers[5])
	if _, err := time.Parse("2006-01-02", from); err != nil {
		return "", "", false
	}
	if _, err := time.Parse("2006-01-02", through); err != nil || from > through {
		return "", "", false
	}
	return from, through, true
}

func walk(value any, visit func(map[string]any)) {
	switch v := value.(type) {
	case map[string]any:
		visit(v)
		for _, child := range v {
			walk(child, visit)
		}
	case []any:
		for _, child := range v {
			walk(child, visit)
		}
	}
}

func huaweiCampaign(body []byte) (string, []catalog.GraduationRule, string, string, error) {
	var descriptions []string
	exam := ""
	for _, script := range configScript.FindAllSubmatch(body, -1) {
		var root any
		if err := json.Unmarshal(script[1], &root); err != nil {
			continue
		}
		walk(root, func(entry map[string]any) {
			if text(entry["key"]) == "FRESH_GRADUATE" {
				for _, item := range array(entry["descList"]) {
					description := PlainText(text(object(item)["description"]))
					if description != "" {
						descriptions = append(descriptions, description)
					}
				}
			}
			description := PlainText(text(entry["description"]))
			if strings.Contains(description, "研发类岗位") && strings.Contains(description, "上机考试") {
				exam = description
			}
		})
	}
	evidence := strings.Join(descriptions, "\n")
	yearMatch := campusYear.FindStringSubmatch(evidence)
	if len(yearMatch) != 2 {
		return "", nil, "", "", ErrSchema
	}
	rules := []catalog.GraduationRule{}
	for _, description := range descriptions {
		from, through, ok := graduationRange(description)
		if !ok {
			continue
		}
		if strings.Contains(description, "国内") && strings.Contains(description, "本科") && strings.Contains(description, "硕士") {
			rules = append(rules, catalog.GraduationRule{Region: "domestic", Education: "bachelor_master", From: from, Through: through, Evidence: description})
		}
		if strings.Contains(description, "国内") && strings.Contains(description, "博士") {
			rules = append(rules, catalog.GraduationRule{Region: "domestic", Education: "doctorate", From: from, Through: through, Evidence: description})
		}
		if strings.Contains(description, "海外") {
			rules = append(rules, catalog.GraduationRule{Region: "overseas", Education: "bachelor_master_doctorate", From: from, Through: through, Evidence: description})
		}
	}
	return yearMatch[1], rules, evidence, exam, nil
}
