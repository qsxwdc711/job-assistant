package matching

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/profile"
	"jobassistant/backend/internal/task"
)

// Recommendation keeps qualifications separate from semantic relevance.
type Recommendation struct {
	ID           string      `json:"id"`
	OwnerID      string      `json:"owner_user_id"`
	ProfileID    string      `json:"profile_version_id"`
	JobID        string      `json:"job_id"`
	JobVersion   int64       `json:"job_version"`
	VariantKey   string      `json:"variant_key"`
	VariantHash  string      `json:"variant_hash"`
	RuleVersion  string      `json:"rule_version"`
	ModelVersion string      `json:"model_version"`
	Eligibility  Eligibility `json:"eligibility"`
	Semantic     Semantic    `json:"semantic"`
	Status       string      `json:"semantic_status"`
	ErrorCode    string      `json:"error_code"`
	TaskID       *string     `json:"task_id"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
	Job          catalog.Job `json:"job"`
}

// Store persists recommendations scoped to immutable candidate and job versions.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type scanner interface{ Scan(...any) error }

const recColumns = `r.id::text,r.owner_user_id::text,r.profile_version_id::text,r.job_id::text,r.job_version,r.variant_key,r.variant_hash,r.rule_version,r.model_version,r.eligibility,r.semantic,r.semantic_status,r.error_code,r.last_task_id::text,r.created_at,r.updated_at`

func scanRec(row scanner) (Recommendation, error) {
	var r Recommendation
	var eligibility, semantic []byte
	err := row.Scan(&r.ID, &r.OwnerID, &r.ProfileID, &r.JobID, &r.JobVersion, &r.VariantKey, &r.VariantHash, &r.RuleVersion, &r.ModelVersion, &eligibility, &semantic, &r.Status, &r.ErrorCode, &r.TaskID, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, identity.ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(eligibility, &r.Eligibility); err != nil {
		return r, err
	}
	err = json.Unmarshal(semantic, &r.Semantic)
	return r, err
}

// terminalTask also covers a shutdown between queue completion and resource-status cleanup.
func terminalTask(ctx context.Context, tx pgx.Tx, r *Recommendation) error {
	if r.Status != "pending" || r.TaskID == nil {
		return nil
	}
	var state, code string
	if err := tx.QueryRow(ctx, `SELECT state,error_code FROM private_tasks WHERE id=$1`, *r.TaskID).Scan(&state, &code); err != nil {
		return err
	}
	if state == "failed" || state == "cancelled" {
		r.Status = state
		r.ErrorCode = code
	}
	return nil
}
func scopeOwner(scope profile.Scope) string {
	if scope.Admin {
		return scope.OwnerID
	}
	return scope.ActorID
}
func (s *Store) authorized(ctx context.Context, scope profile.Scope, action identity.Action, now time.Time) (pgx.Tx, identity.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, identity.User{}, err
	}
	if scope.Admin {
		if _, err = identity.CheckAccess(ctx, tx, scope.ActorID, identity.ManageUsers, "", now); err != nil {
			task.Rollback(tx)
			return nil, identity.User{}, err
		}
	}
	actor, err := identity.CheckAccess(ctx, tx, scope.ActorID, action, scope.OwnerID, now)
	if err != nil {
		task.Rollback(tx)
		return nil, identity.User{}, err
	}
	if action == identity.CreateRecommendations {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,73003))`, scopeOwner(scope)); err != nil {
			task.Rollback(tx)
			return nil, identity.User{}, err
		}
	}
	return tx, actor, nil
}

type facts struct {
	ID, ResumeID, Text string
	Candidate          profile.Candidate
	Approved           bool
}

// currentFacts locks the resume before the selected profile, matching deletion/activation order.
func currentFacts(ctx context.Context, tx pgx.Tx, ownerID string) (facts, error) {
	var f facts
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT v.id::text,v.resume_id::text FROM current_profiles c JOIN profile_versions v ON v.id=c.profile_version_id WHERE c.owner_user_id=$1`, ownerID).Scan(&f.ID, &f.ResumeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, profile.ErrStale
	}
	if err != nil {
		return f, err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT deleted_at IS NULL FROM resumes WHERE id=$1 FOR SHARE`, f.ResumeID).Scan(&active); err != nil {
		return f, err
	}
	if !active {
		return f, profile.ErrStale
	}
	var selected string
	err = tx.QueryRow(ctx, `SELECT v.id::text,v.candidate,v.redacted_text,v.cloud_approved FROM current_profiles c JOIN profile_versions v ON v.id=c.profile_version_id WHERE c.owner_user_id=$1 FOR SHARE OF c,v`, ownerID).Scan(&selected, &raw, &f.Text, &f.Approved)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && selected != f.ID {
		return f, profile.ErrStale
	}
	if err != nil {
		return f, err
	}
	err = json.Unmarshal(raw, &f.Candidate)
	return f, err
}

// Generate evaluates only explicitly selected jobs (at most twenty), then queues optional semantic work.
func (s *Store) Generate(ctx context.Context, scope profile.Scope, ids []string, useModel, retry, configured bool, modelVersion, requestID string, now time.Time) ([]Recommendation, error) {
	if len(ids) == 0 || len(ids) > 20 || scopeOwner(scope) == "" {
		return nil, identity.ErrInvalidInput
	}
	tx, actor, err := s.authorized(ctx, scope, identity.CreateRecommendations, now)
	if err != nil {
		return nil, err
	}
	defer task.Rollback(tx)
	ownerID := scopeOwner(scope)
	ownerUser, err := identity.CheckAccess(ctx, tx, ownerID, identity.ManageApplications, "", now)
	if err != nil {
		return nil, err
	}
	f, err := currentFacts(ctx, tx, ownerID)
	if err != nil {
		return nil, err
	}
	// Lock jobs in a stable order before reading their versioned content.
	rows, err := tx.Query(ctx, `SELECT id FROM jobs WHERE id::text=ANY($1::text[]) ORDER BY id FOR SHARE`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	jobs, err := catalog.LoadJobs(ctx, tx, ids, now)
	if err != nil {
		return nil, err
	}
	if !useModel {
		modelVersion = "rules-only"
	}
	results := []Recommendation{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		job, ok := jobs[id]
		if !ok || !job.Published {
			return nil, identity.ErrNotFound
		}
		for _, variant := range job.Variants {
			eligibility := Evaluate(f.Candidate, job, variant)
			raw, err := json.Marshal(eligibility)
			if err != nil {
				return nil, err
			}
			status := "pending"
			switch {
			case eligibility.Status == "ineligible":
				status = "not_applicable"
			case !useModel:
				status = "not_requested"
			case !configured:
				status = "not_configured"
			case !f.Approved:
				status = "consent_required"
			}
			_, err = tx.Exec(ctx, `INSERT INTO recommendations(owner_user_id,profile_version_id,job_id,job_version,variant_key,variant_hash,rule_version,model_version,eligibility,semantic_status,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11) ON CONFLICT DO NOTHING`, ownerID, f.ID, id, job.Version, variant.Key, VariantHash(variant), RuleVersion, modelVersion, raw, status, now)
			if err != nil {
				return nil, err
			}
			r, err := scanRec(tx.QueryRow(ctx, `SELECT `+recColumns+` FROM recommendations r WHERE owner_user_id=$1 AND profile_version_id=$2 AND job_id=$3 AND job_version=$4 AND variant_key=$5 AND variant_hash=$6 AND rule_version=$7 AND model_version=$8 FOR UPDATE`, ownerID, f.ID, id, job.Version, variant.Key, VariantHash(variant), RuleVersion, modelVersion))
			if err != nil {
				return nil, err
			}
			if eligibility.Status == "ineligible" && r.Eligibility.Status != "ineligible" {
				if _, err = tx.Exec(ctx, `UPDATE recommendations SET eligibility=$2,semantic_status='not_applicable',semantic='{}',error_code='',updated_at=$3 WHERE id=$1`, r.ID, raw, now); err != nil {
					return nil, err
				}
				if r.TaskID != nil {
					if _, err = tx.Exec(ctx, `UPDATE private_tasks SET state='cancelled',error_code='job_closed',lease_until=NULL,lease_token='',updated_at=$2 WHERE id=$1 AND state IN ('queued','running')`, *r.TaskID, now); err != nil {
						return nil, err
					}
				}
				r.Eligibility = eligibility
				r.Status = "not_applicable"
				r.Semantic = Semantic{}
				r.ErrorCode = ""
			}
			if err = terminalTask(ctx, tx, &r); err != nil {
				return nil, err
			}
			enqueue := status == "pending" && (r.TaskID == nil || retry && (r.Status == "failed" || r.Status == "cancelled" || r.Status == "not_configured" || r.Status == "consent_required"))
			if enqueue {
				j, err := task.Enqueue(ctx, tx, actor, ownerUser, "recommend", r.ID, job.Version, now)
				if err != nil {
					return nil, err
				}
				if _, err = tx.Exec(ctx, `UPDATE recommendations SET last_task_id=$2,semantic_status='pending',error_code='',semantic='{}',updated_at=$3 WHERE id=$1`, r.ID, j.ID, now); err != nil {
					return nil, err
				}
				r.TaskID = &j.ID
				r.Status = "pending"
				r.ErrorCode = ""
				r.Semantic = Semantic{}
			}
			r.Job = job
			results = append(results, r)
		}
	}
	if err = identity.AppendAudit(ctx, tx, actor.ID, "recommendations.generate", ownerID, requestID, map[string]any{"job_count": len(seen), "use_model": useModel}, now); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return results, nil
}

// List returns private evidence with current publication/profile checks on every read.
func (s *Store) List(ctx context.Context, scope profile.Scope, id string, limit, offset int, requestID string, now time.Time, modelVersion string) ([]Recommendation, error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return nil, identity.ErrInvalidInput
	}
	tx, _, err := s.authorized(ctx, scope, identity.ReadRecommendations, now)
	if err != nil {
		return nil, err
	}
	defer task.Rollback(tx)
	rows, err := tx.Query(ctx, `SELECT `+recColumns+` FROM recommendations r WHERE ($1='' OR r.owner_user_id::text=$1) AND ($2='' OR r.id::text=$2) ORDER BY r.created_at DESC,r.id LIMIT $3 OFFSET $4`, scopeOwner(scope), id, limit, offset)
	if err != nil {
		return nil, err
	}
	items := []Recommendation{}
	ids := []string{}
	for rows.Next() {
		r, err := scanRec(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, r)
		ids = append(ids, r.JobID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if id != "" && len(items) == 0 {
		return nil, identity.ErrNotFound
	}
	jobs, err := catalog.LoadJobs(ctx, tx, ids, now)
	if err != nil {
		return nil, err
	}
	for i := range items {
		r := &items[i]
		if err = terminalTask(ctx, tx, r); err != nil {
			return nil, err
		}
		r.Job = jobs[r.JobID]
		var current bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM current_profiles WHERE owner_user_id=$1 AND profile_version_id=$2)`, r.OwnerID, r.ProfileID).Scan(&current); err != nil {
			return nil, err
		}
		if !current || r.Job.Version != r.JobVersion || !r.Job.Published || r.RuleVersion != RuleVersion || (r.ModelVersion != "rules-only" && r.ModelVersion != modelVersion) {
			r.Status = "stale"
			r.Semantic = Semantic{}
		} else if r.Job.PublicStatus == "closed" {
			r.Status = "not_applicable"
			r.Semantic = Semantic{}
			r.Eligibility.Status = "ineligible"
			r.Eligibility.Checks = append(r.Eligibility.Checks, Check{Field: "public_status", Status: "ineligible", Reason: "官方岗位已关闭或明确过期", Evidence: r.Job.StatusEvidence})
		}
	}
	if scope.Admin {
		if err = identity.AppendAudit(ctx, tx, scope.ActorID, "recommendations.read", id, requestID, map[string]any{"count": len(items)}, now); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}
