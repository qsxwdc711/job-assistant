package notification

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/task"
)

// RequestDigest queues an administrator-triggered digest at the request's cutoff.
func (s *Store) RequestDigest(ctx context.Context, actor, requestID string, now time.Time) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer task.Rollback(tx)
	if _, err = identity.CheckAccess(ctx, tx, actor, identity.ManageUsers, "", now); err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, "INSERT INTO digest_requests(actor_user_id,created_at) VALUES($1,$2) RETURNING id::text", actor, now).Scan(&id)
	if err != nil {
		return "", err
	}
	if err = identity.AppendAudit(ctx, tx, actor, "notification.digest.request", id, requestID, map[string]bool{}, now); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

// ProcessRequest claims a durable request and rechecks its initiator for every batch.
func (s *Store) ProcessRequest(ctx context.Context, now time.Time) (bool, error) {
	var id, actor string
	var cutoff time.Time
	err := s.pool.QueryRow(ctx, `WITH candidate AS(SELECT id FROM digest_requests WHERE state='queued' OR(state='running' AND lease_until<=$1) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
UPDATE digest_requests r SET state='running',lease_until=$1+interval '120 seconds' FROM candidate c WHERE r.id=c.id RETURNING r.id::text,r.actor_user_id::text,r.created_at`, now).Scan(&id, &actor, &cutoff)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return true, err
	}
	defer task.Rollback(tx)
	_, access := identity.CheckAccess(ctx, tx, actor, identity.ManageUsers, "", now)
	if access != nil {
		if !errors.Is(access, identity.ErrForbidden) && !errors.Is(access, identity.ErrUnauthenticated) {
			return true, access
		}
		_, err = tx.Exec(ctx, "UPDATE digest_requests SET state='cancelled',error_code='permission_changed' WHERE id=$1", id)
		if err != nil {
			return true, err
		}
		return true, tx.Commit(ctx)
	}
	// Each batch rechecks the initiator inside its own transaction. Do not hold
	// a separate account-locking transaction while waiting for those transactions.
	if err = tx.Commit(ctx); err != nil {
		return true, err
	}
	owners, err := s.owners(ctx)
	if err != nil {
		return true, err
	}
	for _, owner := range owners {
		if _, err = s.generate(ctx, Scope{ActorID: actor, OwnerID: owner, Admin: true}, "manual:"+id, cutoff, now); err != nil && !errors.Is(err, identity.ErrForbidden) && !errors.Is(err, identity.ErrUnauthenticated) {
			return true, err
		}
	}
	if _, err = s.pool.Exec(ctx, "UPDATE digest_requests SET state='succeeded',lease_until=NULL WHERE id=$1", id); err != nil {
		return true, err
	}
	return true, nil
}

// RequestCollection enqueues an enabled public source without making HTTP calls.
func (s *Store) RequestCollection(ctx context.Context, actor, source, requestID string, now time.Time) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer task.Rollback(tx)
	if _, err = identity.CheckAccess(ctx, tx, actor, identity.ManageCatalog, "", now); err != nil {
		return "", err
	}
	var official string
	err = tx.QueryRow(ctx, "SELECT official_url FROM sources WHERE code=$1 AND enabled", source).Scan(&official)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", identity.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	u, err := url.Parse(official)
	if err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO scheduled_collections(schedule_key,source_code,domain,available_at,created_at,updated_at) VALUES('manual:'||gen_random_uuid()::text,$1,$2,$3,$3,$3) RETURNING id::text`, source, u.Hostname(), now).Scan(&id)
	if err != nil {
		return "", err
	}
	if err = identity.AppendAudit(ctx, tx, actor, "collection.request", id, requestID, map[string]string{"source": source}, now); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

// AutomationStatus returns the most recent collection outcomes to an administrator.
func (s *Store) AutomationStatus(ctx context.Context, actor string, now time.Time) ([]map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer task.Rollback(tx)
	if _, err = identity.CheckAccess(ctx, tx, actor, identity.ManageCatalog, "", now); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT t.id::text,t.source_code,t.state,t.attempts,t.error_code,t.fetched,t.published,t.updated_at FROM scheduled_collections t ORDER BY t.created_at DESC LIMIT 30`)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for rows.Next() {
		var id, source, state, code string
		var attempts, fetched, published int
		var updated time.Time
		if err = rows.Scan(&id, &source, &state, &attempts, &code, &fetched, &published, &updated); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "source_code": source, "state": state, "attempts": attempts, "error_code": code, "fetched": fetched, "published": published, "updated_at": updated})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return items, tx.Commit(ctx)
}
