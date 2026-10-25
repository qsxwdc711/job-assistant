package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/profile"
)

func TestMokaRequestedVerifiedSourceRegistry(t *testing.T) {
	for _, source := range []string{"voyah-campus", "weride-campus", "kingnet-campus", "ourpalm-campus", "ztgame-campus", "sungrow-campus", "37games-campus", "funplus-campus", "bosch-campus", "ey-campus", "cambricon-campus"} {
		site, ok := mokaConfig(source)
		if !ok || site.source != source || site.company == "" || site.mode != "campus" || site.endpoint != "/api/outer/ats-apply/website/jobs/v2" {
			t.Errorf("verified source %s is not registered: %#v", source, site)
		}
	}
	if site, ok := mokaConfig("ourpalm-campus"); !ok || site.org != "playcrab" || site.path != "/campus-recruitment/ourpalm/43628" {
		t.Fatal("official brand slug must not replace the actual Moka organization ID")
	}
	for _, source := range []string{"qingci-campus", "tesla-campus", "digiloong-campus", "bosch-early-career", "arbitrary-company-campus"} {
		if _, ok := mokaConfig(source); ok {
			t.Errorf("unapproved or unverified source %s was enabled", source)
		}
	}
}

func TestMokaRequestedOptionalListFieldsAndMandatoryIdentity(t *testing.T) {
	for _, mode := range []string{"omitted_optional", "matching_optional", "conflicting_commitment", "conflicting_education", "missing_id", "wrong_org", "wrong_title", "missing_hire_mode", "wrong_status"} {
		t.Run(mode, func(t *testing.T) {
			site, ok := mokaConfig("weride-campus")
			if !ok {
				t.Fatal("verified WeRide source missing")
			}
			sample := object(fixture(t, "moka-requested/weride-campus.json"))
			detail, entry := object(sample["detail"]), object(sample["list"])
			switch mode {
			case "matching_optional":
				entry["commitment"] = detail["commitment"]
			case "conflicting_commitment":
				entry["commitment"] = "实习"
			case "conflicting_education":
				entry["education"], detail["education"] = "本科", "硕士"
			case "missing_id":
				delete(entry, "id")
			case "wrong_org":
				entry["orgId"] = "another-tenant"
			case "wrong_title":
				detail["title"] = "changed software job"
			case "missing_hire_mode":
				delete(entry, "hireMode")
			case "wrong_status":
				detail["status"] = "pause"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == site.path {
					if _, err := fmt.Fprint(w, mokaHome(t, site)); err != nil {
						t.Error(err)
					}
					return
				}
				var q map[string]any
				if err := json.NewDecoder(req.Body).Decode(&q); err != nil || q["orgId"] != site.org {
					t.Error("wrong tenant query")
				}
				if req.URL.Path == site.endpoint {
					mokaWrite(t, w, map[string]any{"jobStats": map[string]any{"orgId": site.org, "total": 1}, "jobs": []any{entry}})
					return
				}
				if req.URL.Path == "/api/outer/ats-apply/website/job" {
					mokaWrite(t, w, detail)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()
			reader := NewReader()
			reader.MokaBase = server.URL
			docs, _, err := reader.Moka(context.Background(), site.source)
			valid := mode == "omitted_optional" || mode == "matching_optional"
			if valid && (err != nil || len(docs) != 1 || docs[0].ExternalID != text(detail["id"])) {
				t.Fatalf("optional absent list fields rejected valid official detail: docs=%d err=%v", len(docs), err)
			}
			if !valid && (!errors.Is(err, ErrSchema) || len(docs) != 0) {
				t.Fatalf("mandatory or present field mismatch accepted: docs=%d err=%v", len(docs), err)
			}
		})
	}
}

func TestMokaRequestedRealTechnicalSamples(t *testing.T) {
	for _, sample := range []struct{ file, source, kind, direction, city string }{
		{"voyah-campus", "voyah-campus", "campus", "backend", "武汉"},
		{"weride-campus", "weride-campus", "campus", "algorithm_ai", "北京"},
		{"ourpalm-campus", "ourpalm-campus", "campus", "mobile", ""},
		{"ztgame-campus", "ztgame-campus", "campus", "mobile", "上海"},
		{"kingnet-campus", "kingnet-campus", "internship", "test", ""},
		{"sungrow-campus", "sungrow-campus", "campus", "mobile", "合肥"},
		{"37games-campus", "37games-campus", "campus", "algorithm_ai", ""},
		{"funplus-campus", "funplus-campus", "campus", "mobile", ""},
		{"ey-it-intern", "ey-campus", "internship", "operations_sre", ""},
	} {
		t.Run(sample.file, func(t *testing.T) {
			site, ok := mokaConfig(sample.source)
			if !ok {
				t.Fatal("verified source missing")
			}
			detail := object(object(fixture(t, "moka-requested/"+sample.file+".json"))["detail"])
			doc, reason, err := mokaDocument(site, detail)
			if err != nil || reason != "" || doc.RecruitmentType != sample.kind || doc.Variants[0].Direction != sample.direction {
				t.Fatalf("real technical sample lost: kind=%s reason=%s err=%v doc=%+v", sample.kind, reason, err, doc)
			}
			if sample.city != "" {
				found := false
				for _, place := range doc.Variants[0].Locations {
					found = found || place.Kind == "domestic" && strings.TrimSuffix(place.City, "市") == sample.city
				}
				if !found {
					t.Fatalf("explicit official city %s lost: %+v", sample.city, doc.Variants[0].Locations)
				}
			}
			if doc.WrittenTest != "unknown" || doc.DeadlineAt != nil {
				t.Fatal("invented deadline or written test")
			}
		})
	}
}

func TestMokaRequestedClosedListEntryIsExcludedWithIdentityIntact(t *testing.T) {
	for _, status := range []string{"closed", "unrecognized"} {
		t.Run(status, func(t *testing.T) {
			site, ok := mokaConfig("ourpalm-campus")
			if !ok {
				t.Fatal("verified source missing")
			}
			sample := object(fixture(t, "moka-requested/ourpalm-campus.json"))
			entry, detail := object(sample["list"]), object(sample["detail"])
			closed := object(object(fixture(t, "moka-requested/ourpalm-closed-control.json"))["list"])
			closed["status"] = status
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case site.path:
					if _, err := fmt.Fprint(w, mokaHome(t, site)); err != nil {
						t.Error(err)
					}
				case site.endpoint:
					mokaWrite(t, w, map[string]any{"jobStats": map[string]any{"orgId": site.org, "total": 2}, "jobs": []any{entry, closed}})
				case "/api/outer/ats-apply/website/job":
					var query map[string]any
					if err := json.NewDecoder(req.Body).Decode(&query); err != nil || query["jobId"] != detail["id"] {
						t.Error("closed job detail should never be fetched")
					}
					mokaWrite(t, w, detail)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			reader := NewReader()
			reader.MokaBase = server.URL
			docs, total, err := reader.Moka(context.Background(), site.source)
			if status == "unrecognized" {
				if !errors.Is(err, ErrSchema) || len(docs) != 0 {
					t.Fatalf("unknown public status must reject the full snapshot: %d %v", len(docs), err)
				}
				return
			}
			if err != nil || total != 2 || len(docs) != 1 || len(reader.Exclusions) != 1 || reader.Exclusions[0].ExternalID != text(closed["id"]) || !strings.Contains(reader.Exclusions[0].Reason, "closed") {
				t.Fatalf("explicitly closed job invalidated unrelated open roles: total=%d docs=%d exclusions=%+v err=%v", total, len(docs), reader.Exclusions, err)
			}
		})
	}
}

func TestMokaRequestedHardAndPreferredStructuredQualifications(t *testing.T) {
	site, ok := mokaConfig("bosch-campus")
	if !ok {
		t.Fatal("verified Bosch source missing")
	}
	for _, mode := range []string{"one_year", "three_years", "unknown_experience", "preferred_experience", "master", "mixed_degree", "mixed_experience", "preferred_section", "preferred_section_suffix", "hard_work_experience", "range_experience", "project_experience"} {
		t.Run(mode, func(t *testing.T) {
			detail := object(object(fixture(t, "moka-requested/bosch-autosar-experience.json"))["detail"])
			detail["jobDescription"] = "软件开发岗位。\n任职要求\n本科及以上学历，计算机专业。"
			detail["customFields"] = map[string]any{"experience": map[string]any{"name": "工作经验要求", "value": "1 year"}}
			switch mode {
			case "three_years":
				object(object(detail["customFields"])["experience"])["value"] = "3 years"
			case "unknown_experience":
				object(object(detail["customFields"])["experience"])["value"] = "Experienced"
			case "preferred_experience":
				object(object(detail["customFields"])["experience"])["name"] = "工作经验优先条件"
				object(object(detail["customFields"])["experience"])["value"] = "3 years"
			case "master":
				detail["customFields"] = map[string]any{"degree": map[string]any{"name": "学历要求", "value": "Master's degree"}}
			case "mixed_degree":
				detail["jobDescription"] = "软件开发岗位，专业不限。"
				detail["customFields"] = map[string]any{"degree": map[string]any{"name": "学历要求", "value": "本科及以上，硕士优先"}}
			case "mixed_experience":
				object(object(detail["customFields"])["experience"])["value"] = "1 year; 3 years preferred"
			case "preferred_section":
				detail["customFields"] = map[string]any{}
				detail["jobDescription"] = "软件开发岗位\n任职要求\n本科及以上学历\n加分项\n硕士及以上学历\n三年工作经验\n"
			case "preferred_section_suffix":
				detail["customFields"] = map[string]any{}
				detail["jobDescription"] = "软件开发岗位\n任职要求\n本科及以上学历\n加分项（满足任意一项即可）\n硕士及以上学历\n三年工作经验\n"
			case "hard_work_experience":
				detail["customFields"] = map[string]any{}
				detail["jobDescription"] = "软件开发岗位。\n本科及以上学历，要求三年工作经验。"
			case "range_experience":
				detail["customFields"] = map[string]any{}
				detail["jobDescription"] = "软件开发岗位。\n本科及以上学历，要求1-3年工作经验。"
			case "project_experience":
				detail["customFields"] = map[string]any{}
				detail["jobDescription"] = "软件开发岗位。\n本科及以上学历，要求三年开源项目开发经验。"
			}
			doc, reason, err := mokaDocument(site, detail)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "three_years" || mode == "unknown_experience" || mode == "hard_work_experience" {
				if reason == "" {
					t.Fatal("hard experienced or unconfirmed role entered current scope")
				}
				return
			}
			if reason != "" || len(doc.Variants) != 1 {
				t.Fatalf("applicable sample rejected: %s", reason)
			}
			zero := 0
			candidate := profile.Candidate{Education: "bachelor", ExperienceMonths: &zero}
			if mode == "mixed_degree" {
				candidate.Education = "associate"
			}
			eligibility := matching.Evaluate(candidate, catalog.Job{Document: doc}, doc.Variants[0])
			field := "experience"
			if mode == "master" || mode == "mixed_degree" || mode == "preferred_section" || mode == "preferred_section_suffix" {
				field = "education"
			}
			excluded := false
			for _, check := range eligibility.Checks {
				excluded = excluded || check.Field == field && check.Status == "ineligible"
			}
			wantExcluded := mode == "one_year" || mode == "master" || mode == "mixed_degree" || mode == "mixed_experience" || mode == "range_experience"
			if excluded != wantExcluded {
				t.Fatalf("qualification did not affect real matching: mode=%s checks=%+v requirements=%s", mode, eligibility.Checks, doc.Variants[0].Requirements)
			}
			if (mode == "preferred_experience" || mode == "preferred_section") && doc.Variants[0].Preferred == "" {
				t.Fatal("preferred conditions were lost rather than separated")
			}
		})
	}
}

func TestMokaRequestedConservativeEnglishAndMixedRoles(t *testing.T) {
	for _, sample := range []struct{ title, want string }{
		{"Software Engineer Intern", "backend"}, {"Embedded Software Engineer", "embedded"}, {"Frontend Engineer", "frontend"}, {"Data Engineer Intern", "data"},
		{"AI Manager", "unknown"}, {"Team Operations & AI Agent 实习生-XC", "unknown"}, {"AI产品经理", "unknown"}, {"AI视频设计师", "unknown"}, {"系统策划", "unknown"}, {"AI游戏策划", "unknown"}, {"硬件测试工程师", "unknown"},
	} {
		if got := mokaDirection(sample.title); got != sample.want {
			t.Errorf("%s direction=%s want=%s", sample.title, got, sample.want)
		}
	}
	site, _ := mokaConfig("bosch-campus")
	detail := object(object(fixture(t, "moka-requested/bosch-mixed-administration.json"))["detail"])
	if doc, reason, err := mokaDocument(site, detail); err != nil || reason == "" || len(doc.Variants) != 0 {
		t.Fatalf("mixed 50 percent administration role was published: %s %v", reason, err)
	}
}

func TestMokaRequestedEnglishInternTypeCannotConflict(t *testing.T) {
	site, ok := mokaConfig("bosch-campus")
	if !ok {
		t.Fatal("verified source missing")
	}
	for _, commitment := range []any{nil, "实习", "全职"} {
		detail := map[string]any{"title": "Software Engineer Intern", "hireMode": json.Number("2"), "commitment": commitment}
		kind, reason := mokaRecruitment(site, detail, "Bachelor degree; currently enrolled students")
		if commitment == "全职" {
			if reason == "" {
				t.Fatal("English Intern title silently published as a full-time campus job")
			}
		} else if kind != "internship" || reason != "" {
			t.Fatalf("explicit public internship title lost: commitment=%v kind=%s reason=%s", commitment, kind, reason)
		}
	}
}

func TestMokaRequestedPhysicalSafetyAndCommercialOperationsAreNotComputerRoles(t *testing.T) {
	for _, raw := range array(fixture(t, "moka-requested/physical-and-business-controls.json")) {
		sample := object(raw)
		detail := object(sample["detail"])
		t.Run(text(detail["title"]), func(t *testing.T) {
			site, ok := mokaConfig(text(sample["source"]))
			if !ok {
				t.Fatal("verified source missing")
			}
			doc, reason, err := mokaDocument(site, detail)
			if err != nil {
				t.Fatal(err)
			}
			if sample["expected_selected"] == true {
				if reason != "" || len(doc.Variants) != 1 {
					t.Fatalf("explicit network security or IT duties lost: %s", reason)
				}
			} else if reason == "" || len(doc.Variants) != 0 {
				t.Fatal("physical safety, customer fulfilment or product management was published as a computer role")
			}
		})
	}
}

func TestMokaRequestedQualificationEvidenceIsDeterministic(t *testing.T) {
	site, _ := mokaConfig("bosch-campus")
	detail := object(object(fixture(t, "moka-requested/bosch-algorithm.json"))["detail"])
	first, reason, err := mokaDocument(site, detail)
	if err != nil || reason != "" {
		t.Fatalf("real Bosch campus algorithm sample: %s %v", reason, err)
	}
	firstBytes, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		next, reason, err := mokaDocument(site, detail)
		nextBytes, encodeErr := json.Marshal(next)
		if err != nil || encodeErr != nil || reason != "" || string(firstBytes) != string(nextBytes) {
			t.Fatal("map iteration or private metadata changes content identity")
		}
	}
	if strings.Contains(first.CampaignEvidence, "成本中心") || strings.Contains(first.CampaignEvidence, "内推奖金") || strings.Contains(first.Variants[0].Requirements, "加分项") {
		t.Fatal("internal metadata or preferred section polluted hard requirements")
	}
}

func TestMokaRequestedPreservesExistingTenantDocuments(t *testing.T) {
	// These hashes were produced by the unmodified canonical normalizer before
	// this batch, not by the implementation being verified here.
	for _, sample := range []struct{ source, file, sha256 string }{
		{"nsfocus-campus", "nsfocus-security", "d41ff934f816ab0de9b26dc4bf5a6114277281c5fca2cf499e6441cac30d828f"},
		{"shokz-campus", "shokz-ai-intern", "78eef89d203cfa9b704ff6ef8f430e14969019f6eb2dba4473f42f54bb73c500"},
		{"shokz-campus", "shokz-master-intern", "dc96c87f0369bff90f8ddbc2ad37068fd8f80c7b9be6103631210260e72d9879"},
	} {
		site, ok := mokaConfig(sample.source)
		if !ok {
			t.Fatal(sample.source)
		}
		doc, reason, err := mokaDocument(site, object(fixture(t, "moka/"+sample.file+".json")))
		if err != nil || reason != "" {
			t.Fatalf("old fixture rejected: %s %v", reason, err)
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if got := hex.EncodeToString(sum[:]); got != sample.sha256 {
			t.Errorf("%s content changed: got %s want %s", sample.file, got, sample.sha256)
		}
	}
}
