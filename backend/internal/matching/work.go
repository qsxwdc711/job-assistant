package matching

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/profile"
	"jobassistant/backend/internal/task"
)

// Work holds only redacted input and the exact variant's JD, in memory for one call.
type Work struct {
	ResumeText, JD string
	Done           bool
}

// VariantJD never includes a sibling variant's cities or requirements.
func VariantJD(job catalog.Job, v catalog.Variant) string {
	locations := []string{}
	for _, place := range v.Locations {
		locations = append(locations, place.Raw)
	}
	return strings.Join([]string{"岗位：" + job.Title, "方向：" + v.Name, "职责：" + v.Responsibilities, "必需条件：" + v.Requirements, "优先条件：" + v.Preferred, "专业说明：" + v.Major.Evidence, "地点：" + strings.Join(locations, "、"), "批次说明：" + job.CampaignEvidence}, "\n")
}

func (s *Store) locked(ctx context.Context, tx pgx.Tx, j task.Job, modelVersion string, now time.Time) (Recommendation, Work, error) {
	if err := task.CheckAccess(ctx, tx, j, identity.CreateRecommendations, now); err != nil {
		return Recommendation{}, Work{}, err
	}
	f, err := currentFacts(ctx, tx, j.OwnerID)
	if errors.Is(err, profile.ErrStale) {
		return Recommendation{}, Work{}, task.ErrCancelled
	}
	if err != nil {
		return Recommendation{}, Work{}, err
	}
	var jobID string
	err = tx.QueryRow(ctx, `SELECT job_id::text FROM recommendations WHERE id=$1 AND owner_user_id=$2`, j.ResourceID, j.OwnerID).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Recommendation{}, Work{}, task.ErrCancelled
	}
	if err != nil {
		return Recommendation{}, Work{}, err
	}
	var lockedID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM jobs WHERE id=$1 FOR SHARE`, jobID).Scan(&lockedID); err != nil {
		return Recommendation{}, Work{}, err
	}
	jobs, err := catalog.LoadJobs(ctx, tx, []string{jobID}, now)
	if err != nil {
		return Recommendation{}, Work{}, err
	}
	job := jobs[jobID]
	r, err := scanRec(tx.QueryRow(ctx, `SELECT `+recColumns+` FROM recommendations r WHERE id=$1 FOR UPDATE`, j.ResourceID))
	if errors.Is(err, identity.ErrNotFound) {
		return r, Work{}, task.ErrCancelled
	}
	if err != nil {
		return r, Work{}, err
	}
	if !f.Approved || r.ProfileID != f.ID || r.TaskID == nil || *r.TaskID != j.ID || r.JobVersion != job.Version || !job.Published || job.PublicStatus == "closed" || r.RuleVersion != RuleVersion || r.ModelVersion != modelVersion || r.Eligibility.Status == "ineligible" {
		return r, Work{}, task.ErrCancelled
	}
	var variant catalog.Variant
	found := false
	for _, v := range job.Variants {
		if v.Key == r.VariantKey && VariantHash(v) == r.VariantHash {
			variant = v
			found = true
			break
		}
	}
	if !found {
		return r, Work{}, task.ErrCancelled
	}
	if err = task.CheckLease(ctx, tx, j, now); err != nil {
		return r, Work{}, err
	}
	return r, Work{ResumeText: f.Text, JD: VariantJD(job, variant), Done: r.Status == "succeeded"}, nil
}

// Prepare rechecks account, selected profile, job content, variant, configuration and lease.
func (s *Store) Prepare(ctx context.Context, j task.Job, version string, now time.Time) (Work, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Work{}, err
	}
	defer task.Rollback(tx)
	_, work, err := s.locked(ctx, tx, j, version, now)
	if err != nil {
		return Work{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Work{}, err
	}
	return work, nil
}

// Complete repeats the guards after the external response, then stores validated quotations.
func (s *Store) Complete(ctx context.Context, j task.Job, version string, result Semantic, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer task.Rollback(tx)
	_, work, err := s.locked(ctx, tx, j, version, now)
	if err != nil {
		return err
	}
	if err = ValidateSemantic(result, work.ResumeText, work.JD); err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE recommendations SET semantic=$2,semantic_status='succeeded',error_code='',updated_at=$3 WHERE id=$1`, j.ResourceID, raw, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Fail marks only the recommendation still assigned to this lease, without publishing private output.
func (s *Store) Fail(ctx context.Context, j task.Job, version, code string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer task.Rollback(tx)
	_, _, err = s.locked(ctx, tx, j, version, now)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE recommendations SET semantic_status='failed',error_code=$2,updated_at=$3 WHERE id=$1`, j.ResourceID, code, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Retire clears a cancelled pending result after the queue transaction has released its lock.
func (s *Store) Retire(ctx context.Context, j task.Job, now time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE recommendations SET semantic_status='cancelled',error_code='cancelled',updated_at=$3 WHERE id=$1 AND last_task_id=$2 AND semantic_status='pending' AND EXISTS(SELECT 1 FROM private_tasks WHERE id=$2 AND state='cancelled')`, j.ResourceID, j.ID, now)
	return err
}
