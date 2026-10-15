package matching

import (
	"context"
	"strings"

	"jobassistant/backend/internal/profile"
)

// Citation separates a model's explanation from the two verbatim supporting inputs.
type Citation struct {
	Statement   string `json:"statement"`
	ResumeQuote string `json:"resume_quote"`
	JDQuote     string `json:"jd_quote"`
}

// Semantic is advisory skill matching; gaps do not override deterministic qualification rules.
type Semantic struct {
	Level     string     `json:"level"`
	Required  []Citation `json:"required"`
	Preferred []Citation `json:"preferred"`
	Gaps      []Citation `json:"gaps"`
}

// Matcher is owned by the matching consumer and implemented by a configured provider.
type Matcher interface {
	Match(context.Context, string, string) (Semantic, error)
	Version() string
	Configured() bool
}

// ValidateSemantic rejects empty, unsupported and fabricated evidence before persistence.
func ValidateSemantic(value Semantic, resume, jd string) error {
	if value.Level != "high" && value.Level != "medium" && value.Level != "low" {
		return profile.ErrInvalidOutput
	}
	if len(value.Required)+len(value.Preferred)+len(value.Gaps) == 0 || len(value.Required)+len(value.Preferred)+len(value.Gaps) > 30 {
		return profile.ErrInvalidOutput
	}
	for _, group := range []struct {
		items []Citation
		gap   bool
	}{{value.Required, false}, {value.Preferred, false}, {value.Gaps, true}} {
		for _, citation := range group.items {
			if strings.TrimSpace(citation.Statement) == "" || len(citation.Statement) > 1200 || strings.TrimSpace(citation.JDQuote) == "" || len(citation.JDQuote) > 2000 || !strings.Contains(jd, citation.JDQuote) {
				return profile.ErrInvalidOutput
			}
			if citation.ResumeQuote == "" {
				if !group.gap {
					return profile.ErrInvalidOutput
				}
			} else if len(citation.ResumeQuote) > 2000 || !strings.Contains(resume, citation.ResumeQuote) {
				return profile.ErrInvalidOutput
			}
		}
	}
	return nil
}
