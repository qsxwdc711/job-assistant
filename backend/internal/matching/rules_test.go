package matching

import (
	"testing"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/profile"
)

func TestEligibilityUsesExplicitHardRequirements(t *testing.T) {
	job := catalog.Job{Document: catalog.Document{RecruitmentType: "campus", MinimumEducation: "unknown", StudentRequired: "unknown"}}
	variant := catalog.Variant{Requirements: "要求硕士及以上学历，Java 优先", Major: catalog.MajorRequirement{Kind: "unrestricted"}}
	candidate := profile.Candidate{Education: "bachelor", EducationRegion: "domestic", Skills: []string{"Go"}}
	result := Evaluate(candidate, job, variant)
	if result.Status != "ineligible" {
		t.Fatalf("master minimum for bachelor: %+v", result)
	}
	variant.Requirements = "本科及以上学历，Java 优先"
	result = Evaluate(candidate, job, variant)
	if result.Status == "ineligible" {
		t.Fatalf("preferred skill excluded candidate: %+v", result)
	}
	variant.Major = catalog.MajorRequirement{Kind: "related", Tags: []string{"computer_science"}, Evidence: "计算机相关专业"}
	candidate.MajorCodes = []string{"communications"}
	if got := Evaluate(candidate, job, variant); got.Status != "needs_confirmation" {
		t.Fatalf("unproven related major: %+v", got)
	}
}

func TestGraduationAndInternshipUnknownsStaySeparate(t *testing.T) {
	job := catalog.Job{Document: catalog.Document{RecruitmentType: "internship", MinimumEducation: "bachelor_or_above", StudentRequired: "yes", MinimumInternshipMonths: intPointer(1), DaysPerWeek: intPointer(5), GraduationPreference: "2027届优先"}}
	variant := catalog.Variant{Major: catalog.MajorRequirement{Kind: "unrestricted"}}
	candidate := profile.Candidate{Education: "bachelor", EducationRegion: "domestic", GraduationDate: "2026-06-30"}
	if got := Evaluate(candidate, job, variant); got.Status != "needs_confirmation" {
		t.Fatalf("unknown attendance: %+v", got)
	}
	candidate.Student = boolPointer(true)
	candidate.InternshipMonths = intPointer(1)
	candidate.DaysPerWeek = intPointer(5)
	if got := Evaluate(candidate, job, variant); got.Status == "ineligible" {
		t.Fatalf("preference treated as exclusion: %+v", got)
	}
	job.GraduationRules = []catalog.GraduationRule{{Region: "all", Education: "bachelor_master", From: "2027-01-01", Through: "2027-12-31", Evidence: "2027年毕业"}}
	variant.Requirements = "毕业时间：2026年1月1日至2026年12月31日"
	if got := Evaluate(candidate, job, variant); got.Status != "needs_confirmation" {
		t.Fatalf("conflicting common conditions: %+v", got)
	}
}

func TestSemanticQuotesMustExistInTheirInputs(t *testing.T) {
	result := Semantic{Level: "high", Required: []Citation{{Statement: "简历体现 Go 项目", ResumeQuote: "Go 服务", JDQuote: "Go 开发"}}}
	if err := ValidateSemantic(result, "实现 Go 服务", "岗位要求 Go 开发"); err != nil {
		t.Fatal(err)
	}
	result.Required[0].ResumeQuote = "不存在的项目"
	if err := ValidateSemantic(result, "实现 Go 服务", "岗位要求 Go 开发"); err == nil {
		t.Fatal("fabricated evidence accepted")
	}
}

func intPointer(v int) *int    { return &v }
func boolPointer(v bool) *bool { return &v }

func TestVariantGraduationRangeAppliesWithoutParentAndPreferredNeverExcludes(t *testing.T) {
	job := catalog.Job{Document: catalog.Document{MinimumEducation: "bachelor_or_above"}}
	c := profile.Candidate{Education: "bachelor", GraduationDate: "2026-06-30"}
	v := catalog.Variant{Requirements: "毕业时间：2027年1月1日至2027年12月31日", Major: catalog.MajorRequirement{Kind: "unrestricted"}}
	if got := Evaluate(c, job, v); got.Status != "ineligible" {
		t.Fatalf("explicit direction graduation ignored: %v", got)
	}
	v.Requirements += "毕业生优先"
	if got := Evaluate(c, job, v); got.Status == "ineligible" {
		t.Fatalf("graduation preference excluded user: %v", got)
	}
	v.Requirements = "毕业时间：2027年13月1日至2027年14月31日"
	if got := Evaluate(c, job, v); got.Status != "needs_confirmation" {
		t.Fatalf("invalid official date guessed: %v", got)
	}
}
