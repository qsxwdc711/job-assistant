// Package matching keeps deterministic qualification rules separate from semantic assistance.
package matching

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/profile"
)

const RuleVersion = "qualification-v1"

// Check explains one qualification and the official evidence that supports it.
type Check struct {
	Field    string `json:"field"`
	Status   string `json:"status"`
	Reason   string `json:"reason"`
	Evidence string `json:"evidence"`
}

// Eligibility never represents a probability of receiving an interview or offer.
type Eligibility struct {
	Status string  `json:"status"`
	Checks []Check `json:"checks"`
}

var clauses = regexp.MustCompile(`[，,；;。\n]`)
var minExperience = regexp.MustCompile(`(?:至少|不少于|最低|要求|具备|具有|需)\s*([1-9][0-9]?)\s*年(?:以上|及以上)?(?:相关)?工作经验|([1-9][0-9]?)\s*年(?:及以上|以上)(?:相关)?工作经验`)
var graduationDates = regexp.MustCompile(`毕业(?:时间|日期|范围)[：:\s]*([12][0-9]{3})年([0-9]{1,2})月([0-9]{1,2})日[至到—~～-]+([12][0-9]{3})年([0-9]{1,2})月([0-9]{1,2})日`)

// VariantHash participates in cache identity so directions and their cities cannot be mixed.
func VariantHash(v catalog.Variant) string {
	// Variant contains only concrete JSON-compatible fields; marshaling cannot fail.
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Evaluate uses known conditions and preserves uncertainty instead of guessing missing facts.
func Evaluate(c profile.Candidate, job catalog.Job, v catalog.Variant) Eligibility {
	result := Eligibility{Status: "eligible", Checks: []Check{}}
	add := func(field, status, reason, evidence string) {
		result.Checks = append(result.Checks, Check{field, status, reason, evidence})
		if status == "ineligible" {
			result.Status = status
		} else if status == "needs_confirmation" && result.Status != "ineligible" {
			result.Status = status
		}
	}
	if job.PublicStatus == "closed" {
		add("public_status", "ineligible", "官方岗位已关闭或明确过期", job.StatusEvidence)
	}
	rank := map[string]int{"associate": 1, "bachelor": 2, "master": 3, "doctorate": 4}
	minimum := 0
	degreeEvidence := ""
	if job.MinimumEducation == "bachelor_or_above" {
		minimum = 2
		degreeEvidence = "官方结构化条件：本科及以上"
	}
	for _, clause := range clauses.Split(v.Requirements, -1) {
		if strings.Contains(clause, "优先") {
			continue
		}
		level := 0
		switch {
		case strings.Contains(clause, "博士及以上") || strings.Contains(clause, "博士学历") || strings.Contains(clause, "学历要求：博士"):
			level = 4
		case strings.Contains(clause, "硕士及以上") || strings.Contains(clause, "硕士以上") || strings.Contains(clause, "研究生及以上"):
			level = 3
		case strings.Contains(clause, "本科及以上") || strings.Contains(clause, "本科以上"):
			level = 2
		}
		if level > minimum {
			minimum = level
			degreeEvidence = strings.TrimSpace(clause)
		}
	}
	switch {
	case minimum == 0:
		add("education", "needs_confirmation", "官方未明确最低学历，需查看原文确认", "")
	case rank[c.Education] == 0:
		add("education", "needs_confirmation", "尚未确认适用学历", degreeEvidence)
	case rank[c.Education] < minimum:
		add("education", "ineligible", "已确认学历低于明确最低要求", degreeEvidence)
	default:
		add("education", "eligible", "满足已知最低学历", degreeEvidence)
	}
	graduationCheck(c, job, v, add)
	switch v.Major.Kind {
	case "unrestricted":
		add("major", "eligible", "官方明确专业不限", v.Major.Evidence)
	case "preferred":
		add("major", "eligible", "专业属于优先条件，不排除其他专业", v.Major.Evidence)
	case "unknown", "":
		add("major", "needs_confirmation", "官方未披露明确专业范围", v.Major.Evidence)
	default:
		matched := false
		for _, code := range c.MajorCodes {
			if slices.Contains(v.Major.Tags, code) {
				matched = true
			}
		}
		if matched {
			add("major", "eligible", "已确认专业命中官方范围", v.Major.Evidence)
		} else if len(c.MajorCodes) == 0 || v.Major.Kind == "related" {
			add("major", "needs_confirmation", "缺少证据确认本人专业属于适用范围", v.Major.Evidence)
		} else {
			add("major", "ineligible", "已确认专业不在明确限定范围内", v.Major.Evidence)
		}
	}
	experience := 0
	experienceEvidence := ""
	for _, clause := range clauses.Split(v.Requirements, -1) {
		if strings.Contains(clause, "优先") {
			continue
		}
		if match := minExperience.FindStringSubmatch(clause); len(match) > 0 {
			value := match[1]
			if value == "" {
				value = match[2]
			}
			// minExperience restricts this value to 1..99, always within an int.
			years, _ := strconv.Atoi(value)
			if years*12 > experience {
				experience = years * 12
				experienceEvidence = strings.TrimSpace(clause)
			}
		}
	}
	if experience > 0 {
		if c.ExperienceMonths == nil {
			add("experience", "needs_confirmation", "尚未确认工作经验时长", experienceEvidence)
		} else if *c.ExperienceMonths < experience {
			add("experience", "ineligible", "工作经验不足明确最低要求", experienceEvidence)
		} else {
			add("experience", "eligible", "满足已知工作经验要求", experienceEvidence)
		}
	} else if job.RecruitmentType == "early_career" {
		add("experience", "needs_confirmation", "社招经验适用范围需核实", "")
	}
	if job.StudentRequired == "yes" {
		if c.Student == nil {
			add("student", "needs_confirmation", "尚未确认在校身份", "官方要求在校生")
		} else if !*c.Student {
			add("student", "ineligible", "已确认非在校生", "官方要求在校生")
		} else {
			add("student", "eligible", "已确认在校身份", "官方要求在校生")
		}
	} else if job.RecruitmentType == "internship" {
		add("student", "needs_confirmation", "官方未明确在校身份限制", "")
	}
	for _, item := range []struct {
		field, label     string
		required, actual *int
	}{{"internship_months", "实习持续月数", job.MinimumInternshipMonths, c.InternshipMonths}, {"days_per_week", "每周出勤天数", job.DaysPerWeek, c.DaysPerWeek}} {
		if item.required == nil {
			if job.RecruitmentType == "internship" {
				add(item.field, "needs_confirmation", "官方未明确"+item.label, "")
			}
			continue
		}
		evidence := fmt.Sprintf("官方最低%s：%d", item.label, *item.required)
		if item.actual == nil {
			add(item.field, "needs_confirmation", "尚未确认本人"+item.label, evidence)
		} else if *item.actual < *item.required {
			add(item.field, "ineligible", item.label+"低于明确要求", evidence)
		} else {
			add(item.field, "eligible", "满足已知"+item.label, evidence)
		}
	}
	return result
}

func graduationCheck(c profile.Candidate, job catalog.Job, v catalog.Variant, add func(string, string, string, string)) {
	var match []string
	for _, clause := range clauses.Split(v.Requirements, -1) {
		if !strings.Contains(clause, "优先") {
			if found := graduationDates.FindStringSubmatch(clause); len(found) > 0 {
				match = found
				break
			}
		}
	}
	from, through := "", ""
	if len(match) > 0 {
		values := make([]int, 0, 6)
		for _, value := range match[1:] {
			n, err := strconv.Atoi(value)
			if err != nil {
				add("graduation", "needs_confirmation", "岗位毕业范围无法可靠识别", match[0])
				return
			}
			values = append(values, n)
		}
		from = fmt.Sprintf("%04d-%02d-%02d", values[0], values[1], values[2])
		through = fmt.Sprintf("%04d-%02d-%02d", values[3], values[4], values[5])
		_, a := time.Parse("2006-01-02", from)
		_, b := time.Parse("2006-01-02", through)
		if a != nil || b != nil || from > through {
			add("graduation", "needs_confirmation", "岗位毕业范围无效，需核实原文", match[0])
			return
		}
	}
	if len(job.GraduationRules) == 0 {
		if from != "" && c.GraduationDate != "" {
			if c.GraduationDate >= from && c.GraduationDate <= through {
				add("graduation", "eligible", "毕业日期满足该方向的明确范围", match[0])
			} else {
				add("graduation", "ineligible", "毕业日期不在该方向的明确范围内", match[0])
			}
			return
		}
		add("graduation", "needs_confirmation", "官方未明确硬性毕业范围；届数优先不作为排除条件", job.GraduationPreference)
		return
	}
	applicable := []catalog.GraduationRule{}
	for _, rule := range job.GraduationRules {
		region := rule.Region == "all" || rule.Region == c.EducationRegion
		education := rule.Education == "unknown" || rule.Education == "" || slices.Contains(strings.Split(rule.Education, "_"), c.Education)
		if region && education {
			applicable = append(applicable, rule)
		}
	}
	if len(applicable) == 0 || c.GraduationDate == "" {
		add("graduation", "needs_confirmation", "需要确认境内外、学历及毕业日期的适用范围", job.CampaignEvidence)
		return
	}
	if len(match) > 0 {
		overlap := false
		for _, rule := range applicable {
			if from <= rule.Through && through >= rule.From {
				overlap = true
			}
		}
		if !overlap {
			add("graduation", "needs_confirmation", "岗位与共同适用批次的毕业范围冲突，需核实", match[0]+"\n"+job.CampaignEvidence)
			return
		}
	}
	for _, rule := range applicable {
		if c.GraduationDate >= rule.From && c.GraduationDate <= rule.Through && (from == "" || (c.GraduationDate >= from && c.GraduationDate <= through)) {
			add("graduation", "eligible", "毕业日期满足适用的已知范围", rule.Evidence)
			return
		}
	}
	add("graduation", "ineligible", "已确认毕业日期不在适用的明确范围内", job.CampaignEvidence)
}
