package profile

import (
	"regexp"
	"sort"
	"strings"
)

var emailPattern = regexp.MustCompile(`(?i)[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9.-]+\.[a-z]{2,}`)
var phonePattern = regexp.MustCompile(`(?:\+?86[- ]?)?1[3-9][0-9](?:[- ]?[0-9]){8}|(?:\+?[0-9]{1,3}[- ])?\(?0[0-9]{2,3}\)?[- ]?[0-9]{7,8}`)
var identityPattern = regexp.MustCompile(`\b[1-9][0-9]{16}[0-9Xx]\b|(?i)\b[A-Z][0-9]{7,9}\b`)
var nameLine = regexp.MustCompile(`(?mi)^\s*(?:姓名|name)\s*[:：]\s*([^\n|｜,，]{1,60})`)
var shortName = regexp.MustCompile(`^[\p{Han}]{2,4}$|^[A-Z][a-z]+(?:[ -][A-Z][a-z]+){1,3}$`)
var addressLine = regexp.MustCompile(`(?mi)^.*(?:详细住址|家庭住址|通讯地址|现住址|联系地址|地址|address)\s*[:：].*$`)
var streetLine = regexp.MustCompile(`(?m)^.*[省市区县].*(?:路|街|巷)[^\n]{0,40}[0-9一二三四五六七八九十]+号[^\n]*$`)

// Redact removes direct identifiers locally; useful qualifications may still be identifying.
func Redact(text string, names []string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	known := append([]string(nil), names...)
	for _, match := range nameLine.FindAllStringSubmatch(text, -1) {
		known = append(known, strings.TrimSpace(match[1]))
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 1 && shortName.MatchString(strings.TrimSpace(lines[0])) {
		end := min(len(lines), 6)
		if emailPattern.MatchString(strings.Join(lines[1:end], "\n")) || phonePattern.MatchString(strings.Join(lines[1:end], "\n")) {
			known = append(known, strings.TrimSpace(lines[0]))
		}
	}
	sort.Slice(known, func(i, j int) bool { return len(known[i]) > len(known[j]) })
	for _, name := range known {
		if name = strings.TrimSpace(name); len([]rune(name)) >= 2 {
			text = strings.ReplaceAll(text, name, "[姓名已移除]")
		}
	}
	text = addressLine.ReplaceAllString(text, "[地址已移除]")
	text = streetLine.ReplaceAllString(text, "[地址已移除]")
	text = emailPattern.ReplaceAllString(text, "[邮箱已移除]")
	text = identityPattern.ReplaceAllString(text, "[证件已移除]")
	return strings.TrimSpace(phonePattern.ReplaceAllString(text, "[电话已移除]"))
}
