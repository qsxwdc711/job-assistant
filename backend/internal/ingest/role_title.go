package ingest

import "strings"

// nontechnicalTitle rejects explicit business and creative titles before a
// provider classifies their remaining technical role keywords.
func nontechnicalTitle(title string) bool {
	title = strings.ToLower(title)
	if (strings.Contains(title, "运营") && !strings.Contains(title, "开发") && !strings.Contains(title, "算法") && !strings.Contains(title, "研究") && !strings.Contains(title, "工程师")) || (strings.Contains(title, "产品") && !strings.Contains(title, "开发") && !strings.Contains(title, "研发") && !strings.Contains(title, "工程师")) {
		return true
	}
	for _, word := range []string{"美术", "特效", "视频", "动画", "音效", "ui设计", "gui设计", "销售", "市场", "营销", "人力", "招聘", "财务", "行政", "硬件测试", "可靠性测试", "声学测试", "质量检验", "项目经理", "产品经理", "售前", "策划", "manager", "team operations", "administration", "recruiter", "marketing", "finance", "sales"} {
		if strings.Contains(title, word) {
			return true
		}
	}
	return false
}
