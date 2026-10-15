package application

import (
	"context"
	"time"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
)

// SetFavorite idempotently saves or removes a job for the authorized owner.
func (s *Store) SetFavorite(ctx context.Context, scope Scope, jobID string, saved bool, requestID string, now time.Time) error {
	tx, actor, err := s.authorized(ctx, scope, now)
	if err != nil {
		return err
	}
	defer rollback(tx)
	owner := ownerFor(scope)
	if owner == "" {
		return identity.ErrInvalidInput
	}
	if saved {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM jobs WHERE id::text=$1 AND (published OR $2))", jobID, actor.EffectiveRole(now) == identity.RoleAdmin).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return identity.ErrNotFound
		}
		if _, err := tx.Exec(ctx, "INSERT INTO favorites(owner_user_id,job_id,created_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", owner, jobID, now); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, "DELETE FROM favorites WHERE owner_user_id::text=$1 AND job_id::text=$2", owner, jobID); err != nil {
			return err
		}
	}
	if actor.EffectiveRole(now) == identity.RoleAdmin {
		if err := identity.AppendAudit(ctx, tx, actor.ID, "favorite.set", jobID, requestID, map[string]any{"owner_user_id": owner, "saved": saved}, now); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Favorites returns private saved jobs, including jobs that subsequently closed.
func (s *Store) Favorites(ctx context.Context, scope Scope, limit, offset int, now time.Time) ([]Favorite, error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return nil, identity.ErrInvalidInput
	}
	tx, _, err := s.authorized(ctx, scope, now)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	owner := ownerFor(scope)
	if owner == "" {
		return nil, identity.ErrInvalidInput
	}
	items := []Favorite{}
	ids := []string{}
	rows, err := tx.Query(ctx, "SELECT job_id::text,created_at FROM favorites WHERE owner_user_id::text=$1 ORDER BY created_at DESC,job_id LIMIT $2 OFFSET $3", owner, limit, offset)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var f Favorite
		if err := rows.Scan(&id, &f.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		items = append(items, f)
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
	for i, id := range ids {
		job, ok := jobs[id]
		if !ok {
			return nil, identity.ErrNotFound
		}
		items[i].Job = job
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}
