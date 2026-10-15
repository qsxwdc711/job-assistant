package application

import (
	"context"
	"fmt"
	"time"

	"jobassistant/backend/internal/identity"
)

// JobState contains only the current account's button state for a requested job.
type JobState struct {
	JobID    string `json:"job_id"`
	Favorite bool   `json:"favorite"`
	Recorded bool   `json:"recorded"`
}

// JobStates reads at most 100 validated job UUIDs, preserving request order.
// ActorID is the owner even when the account is an administrator.
func (s *Store) JobStates(ctx context.Context, actorID string, jobIDs []string, now time.Time) ([]JobState, error) {
	if len(jobIDs) == 0 || len(jobIDs) > 100 {
		return nil, identity.ErrInvalidInput
	}
	tx, _, err := s.authorized(ctx, Scope{ActorID: actorID}, now)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	rows, err := tx.Query(ctx, `SELECT ids.job_id::uuid::text,
EXISTS(SELECT 1 FROM favorites f WHERE f.owner_user_id=$1 AND f.job_id=ids.job_id::uuid),
EXISTS(SELECT 1 FROM applications a WHERE a.owner_user_id=$1 AND a.job_id=ids.job_id::uuid)
FROM unnest($2::text[]) WITH ORDINALITY AS ids(job_id,position) ORDER BY ids.position`, actorID, jobIDs)
	if err != nil {
		return nil, fmt.Errorf("query personal job states: %w", err)
	}
	items := make([]JobState, 0, len(jobIDs))
	for rows.Next() {
		var item JobState
		if err := rows.Scan(&item.JobID, &item.Favorite, &item.Recorded); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan personal job state: %w", err)
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("read personal job states: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit personal job states: %w", err)
	}
	return items, nil
}
