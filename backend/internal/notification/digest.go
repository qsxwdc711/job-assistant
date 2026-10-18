package notification

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/task"
)

func safeText(value string, limit int) string {
	value = strings.NewReplacer("<", "＜", ">", "＞", "\r", " ", "\n", " ").Replace(value)
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}

// Render consumes only the allowlisted public Item projection.
func Render(items []Item, cutoff time.Time, test bool) string {
	if test {
		return "求职助手：飞书连接测试。此消息不含简历或个人投递信息。"
	}
	ordered := slices.Clone(items)
	slices.SortStableFunc(ordered, func(a, b Item) int {
		if a.Priority != b.Priority {
			return a.Priority - b.Priority
		}
		return strings.Compare(a.JobID, b.JobID)
	})
	unique := ordered[:0]
	seen := map[string]bool{}
	for _, item := range ordered {
		if !seen[item.JobID] {
			seen[item.JobID] = true
			unique = append(unique, item)
		}
	}
	limit := min(10, len(unique))
	for {
		var text strings.Builder
		fmt.Fprintf(&text, "求职助手 · %s 岗位摘要\n共 %d 个机会；资格请结合官方条件确认。\n", cutoff.In(task.Shanghai).Format("01-02 15:04"), len(unique))
		for i, item := range unique[:limit] {
			deadline := item.Deadline
			if deadline == "" {
				deadline = "未公布"
			}
			condition := ""
			if item.NeedsConfirmation {
				condition = " · 条件待确认"
			}
			fmt.Fprintf(&text, "\n%d. %s · %s\n%s · 截止 %s%s\n官方：%s\n详情：%s\n", i+1, safeText(item.Company, 60), safeText(item.Title, 100), safeText(strings.Join(item.Locations, " / "), 120), safeText(deadline, 40), condition, item.OfficialURL, item.DetailsURL)
		}
		if len(unique) > limit {
			fmt.Fprintf(&text, "\n另有 %d 个机会，请登录站内查看。", len(unique)-limit)
		}
		if len(unique) > 0 {
			text.WriteString("\n站内通知：" + strings.Split(unique[0].DetailsURL, "/jobs/")[0] + "/notifications")
		}
		body := text.String()
		payload, err := encodeMessage(body, strings.Repeat("a", 256), cutoff)
		if err == nil && len(payload) <= MaxMessageBytes-256 {
			return body
		}
		if limit == 0 {
			return "求职助手：有新的岗位机会，请登录站内通知查看。"
		}
		limit--
	}
}

func deadlineWindow(at *time.Time, cutoff time.Time) int {
	if at == nil || !at.After(cutoff) {
		return 0
	}
	delta := at.Sub(cutoff)
	for _, days := range []int{1, 3, 7} {
		if delta <= time.Duration(days)*24*time.Hour {
			return days
		}
	}
	return 0
}

func importantChange(before, after catalog.Document) (bool, error) {
	clean := func(doc catalog.Document) ([]byte, error) {
		doc.CampaignEvidence = ""
		doc.StatusEvidence = ""
		doc.Publisher = ""
		return json.Marshal(doc)
	}
	previous, err := clean(before)
	if err != nil {
		return false, err
	}
	current, err := clean(after)
	if err != nil {
		return false, err
	}
	return string(previous) != string(current), nil
}
