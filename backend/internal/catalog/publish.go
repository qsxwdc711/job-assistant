package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"jobassistant/backend/internal/identity"
)

// Evidence records a public read's provenance without request credentials or personal data.
type Evidence struct {
	URL       string    `json:"url"`
	SHA256    string    `json:"sha256"`
	Bytes     int       `json:"bytes"`
	FetchedAt time.Time `json:"fetched_at"`
}

// Exclusion identifies an observed role that requires scope review, without declaring it closed.
type Exclusion struct {
	CampaignKey string
	ExternalID  string
	Reason      string
}

// BeginRun records a system collection attempt. A newer attempt supersedes an older publisher.
func (s *Store) BeginRun(ctx context.Context, code string, now time.Time) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer rollback(tx)
	var enabled bool
	if err := tx.QueryRow(ctx, "SELECT enabled FROM sources WHERE code=$1 FOR UPDATE", code).Scan(&enabled); err != nil {
		return "", err
	}
	if !enabled {
		return "", identity.ErrForbidden
	}
	var id string
	if err := tx.QueryRow(ctx, "INSERT INTO ingest_runs(source_code,started_at) VALUES($1,$2) RETURNING id::text", code, now).Scan(&id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, "UPDATE sources SET current_run_id=$2,last_status='running',last_checked_at=$3,last_error='' WHERE code=$1", code, id, now); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func validateDocument(doc Document, source Source) error {
	if doc.SourceCode != source.Code || doc.CompanyCode != source.CompanyCode || doc.ExternalID == "" || len(doc.ExternalID) > 160 || doc.CampaignKey == "" || len(doc.CampaignKey) > 160 || strings.TrimSpace(doc.Title) == "" || len(doc.Variants) == 0 {
		return fmt.Errorf("%w: incomplete job identity", identity.ErrInvalidInput)
	}
	for _, pair := range []struct{ value, allowed string }{{doc.PublicStatus, "open|closed|unknown"}, {doc.WrittenTest, "yes|no|unknown"}, {doc.RecruitmentType, "campus|internship|early_career"}, {doc.ApplyLevel, "job|campaign|company"}} {
		if !slices.Contains(strings.Split(pair.allowed, "|"), pair.value) {
			return identity.ErrInvalidInput
		}
	}
	root, err := url.Parse(source.OfficialURL)
	if err != nil {
		return err
	}
	for _, raw := range []string{doc.SourceURL, doc.ApplyURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() != root.Hostname() {
			return fmt.Errorf("%w: official job URL required", identity.ErrInvalidInput)
		}
	}
	if doc.DeadlineDate != "" {
		if _, err := time.Parse("2006-01-02", doc.DeadlineDate); err != nil {
			return identity.ErrInvalidInput
		}
	}
	keys := map[string]bool{}
	for _, v := range doc.Variants {
		if v.Key == "" || keys[v.Key] {
			return fmt.Errorf("%w: invalid variant identity", identity.ErrInvalidInput)
		}
		keys[v.Key] = true
	}
	return nil
}

// Publish atomically saves a fully validated source result; absence never closes old jobs.
func (s *Store) Publish(ctx context.Context, runID string, documents []Document, fetchedCount int, evidence []Evidence, finished time.Time, exclusions ...Exclusion) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var sourceCode string
	var started time.Time
	if err := tx.QueryRow(ctx, "SELECT source_code,started_at FROM ingest_runs WHERE id=$1 AND status='running' FOR UPDATE", runID).Scan(&sourceCode, &started); err != nil {
		return err
	}
	var source Source
	var current string
	if err := tx.QueryRow(ctx, "SELECT code,company_code,official_url,enabled,current_run_id::text FROM sources WHERE code=$1 FOR UPDATE", sourceCode).Scan(&source.Code, &source.CompanyCode, &source.OfficialURL, &source.Enabled, &current); err != nil {
		return err
	}
	if !source.Enabled {
		return identity.ErrForbidden
	}
	if current != runID {
		return fmt.Errorf("superseded collection run")
	}
	if evidence == nil {
		evidence = []Evidence{}
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	changed := 0
	for _, doc := range documents {
		if err := validateDocument(doc, source); err != nil {
			return err
		}
		key := doc.CampaignKey + "\x00" + doc.ExternalID
		if seen[key] {
			return fmt.Errorf("duplicate source identity")
		}
		seen[key] = true
		rules := doc.GraduationRules
		if rules == nil {
			rules = []GraduationRule{}
		}
		doc.GraduationRules = rules
		rulesJSON, err := json.Marshal(rules)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO campaigns(source_code,campaign_key,name,graduation_rules,evidence) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(source_code,campaign_key) DO UPDATE SET name=excluded.name,graduation_rules=excluded.graduation_rules,evidence=excluded.evidence`, sourceCode, doc.CampaignKey, doc.CampaignName, rulesJSON, doc.CampaignEvidence); err != nil {
			return err
		}
		data, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("encode job content: %w", err)
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		var oldHash string
		err = tx.QueryRow(ctx, "SELECT content_hash FROM jobs WHERE source_code=$1 AND campaign_key=$2 AND external_id=$3 FOR UPDATE", sourceCode, doc.CampaignKey, doc.ExternalID).Scan(&oldHash)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var id string
		var version int64
		var deadline any
		if doc.DeadlineDate != "" {
			deadline = doc.DeadlineDate
		}
		err = tx.QueryRow(ctx, `INSERT INTO jobs AS j(company_code,source_code,campaign_key,external_id,title,recruitment_type,public_status,written_test,deadline_date,deadline_at,data,content_hash,verified_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
		ON CONFLICT(source_code,campaign_key,external_id) DO UPDATE SET title=excluded.title,recruitment_type=excluded.recruitment_type,
		public_status=excluded.public_status,written_test=excluded.written_test,deadline_date=excluded.deadline_date,deadline_at=excluded.deadline_at,
		data=excluded.data,content_hash=excluded.content_hash,verified_at=excluded.verified_at,verification_status='verified',
		version=CASE WHEN j.content_hash=excluded.content_hash THEN j.version ELSE j.version+1 END,
		updated_at=CASE WHEN j.content_hash=excluded.content_hash THEN j.updated_at ELSE excluded.updated_at END
		RETURNING id::text,version`, doc.CompanyCode, sourceCode, doc.CampaignKey, doc.ExternalID, doc.Title, doc.RecruitmentType, doc.PublicStatus, doc.WrittenTest, deadline, doc.DeadlineAt, data, hash, finished).Scan(&id, &version)
		if err != nil {
			return err
		}
		if oldHash == hash {
			continue
		}
		changed++
		if _, err := tx.Exec(ctx, "INSERT INTO job_versions(job_id,version,content_hash,data,evidence,created_at) VALUES($1,$2,$3,$4,$5,$6)", id, version, hash, data, evidenceJSON, finished); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM job_variants WHERE job_id=$1", id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM job_tags WHERE job_id=$1", id); err != nil {
			return err
		}
		for _, variant := range doc.Variants {
			variantJSON, err := json.Marshal(variant)
			if err != nil {
				return err
			}
			tags := variant.Major.Tags
			if tags == nil {
				tags = []string{}
			}
			if _, err := tx.Exec(ctx, "INSERT INTO job_variants(job_id,variant_key,direction,major_kind,major_tags,data) VALUES($1,$2,$3,$4,$5,$6)", id, variant.Key, variant.Direction, variant.Major.Kind, tags, variantJSON); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "INSERT INTO job_tags(job_id,kind,code) VALUES($1,'direction',$2) ON CONFLICT DO NOTHING", id, variant.Direction); err != nil {
				return err
			}
			for _, place := range variant.Locations {
				if _, err := tx.Exec(ctx, "INSERT INTO job_locations(job_id,variant_key,code,kind,country,province,city,raw) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING", id, variant.Key, place.Code, place.Kind, place.Country, place.Province, place.City, place.Raw); err != nil {
					return err
				}
			}
		}
	}
	for _, excluded := range exclusions {
		if excluded.ExternalID == "" || excluded.CampaignKey == "" || seen[excluded.CampaignKey+"\x00"+excluded.ExternalID] {
			return fmt.Errorf("contradictory source scope result")
		}
		rows, err := tx.Query(ctx, "UPDATE jobs SET published=false,verification_status='needs_review',review_note=$4 WHERE source_code=$1 AND campaign_key=$2 AND external_id=$3 AND published RETURNING id::text", sourceCode, excluded.CampaignKey, excluded.ExternalID, "自动核验："+excluded.Reason)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.Exec(ctx, "INSERT INTO audit_events(action,target_id,request_id,metadata,created_at) VALUES('catalog.scope.review',$1,$2,jsonb_build_object('source_code',$3::text),$4)", id, runID, sourceCode, finished); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(ctx, "UPDATE jobs SET verification_status='needs_verification' WHERE source_code=$1 AND verified_at<$2 AND verification_status<>'needs_review'", sourceCode, finished); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE ingest_runs SET status='succeeded',finished_at=$2,fetched_count=$3,published_count=$4,changed_count=$5,evidence=$6 WHERE id=$1", runID, finished, fetchedCount, len(documents), changed, evidenceJSON); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE sources SET last_status='succeeded',last_error='',last_checked_at=$2,last_succeeded_at=$2,last_success_started_at=$3 WHERE code=$1", sourceCode, finished, started); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit collection: %w", err)
	}
	return nil
}

// FailRun records failure without mutating published job content or public status.
func (s *Store) FailRun(ctx context.Context, runID, code string, restricted bool, finished time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	status := "failed"
	if restricted {
		status = "restricted"
	}
	var sourceCode string
	if err := tx.QueryRow(ctx, "UPDATE ingest_runs SET status=$2,error_code=$3,finished_at=$4 WHERE id=$1 RETURNING source_code", runID, status, code, finished).Scan(&sourceCode); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE sources SET last_status=$2,last_error=$3,last_checked_at=$4 WHERE code=$1 AND current_run_id=$5", sourceCode, status, code, finished, runID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
