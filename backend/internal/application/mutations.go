package application

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"jobassistant/backend/internal/identity"
)

// Create inserts the owner's unique job record and its first event atomically.
func (s *Store) Create(ctx context.Context, scope Scope, input Input, requestID string, now time.Time) (Record, error) {
	if input.Status == "" {
		input.Status = "draft"
	}
	if err := validate(input); err != nil {
		return Record{}, err
	}
	if input.JobID == "" || input.Version != 0 {
		return Record{}, identity.ErrInvalidInput
	}
	tx, actor, err := s.authorized(ctx, scope, now)
	if err != nil {
		return Record{}, err
	}
	defer rollback(tx)
	owner := ownerFor(scope)
	if owner == "" {
		return Record{}, identity.ErrInvalidInput
	}
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM jobs WHERE id::text=$1 AND (published OR $2))", input.JobID, actor.EffectiveRole(now) == identity.RoleAdmin).Scan(&exists); err != nil {
		return Record{}, err
	}
	if !exists {
		return Record{}, identity.ErrNotFound
	}
	var id string
	err = tx.QueryRow(ctx, "INSERT INTO applications(owner_user_id,job_id,status,notes,next_action,next_action_at,created_at,updated_at,resume_version_id) VALUES($1,$2,$3,$4,$5,$6,$7,$7,(SELECT profile_version_id FROM current_profiles WHERE owner_user_id=$1)) RETURNING id::text", owner, input.JobID, input.Status, input.Notes, input.NextAction, input.NextActionAt, now).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Record{}, ErrAlreadyExists
	}
	if err != nil {
		return Record{}, err
	}
	record, err := readRecord(ctx, tx, scope, actor, id, false, now)
	if err != nil {
		return Record{}, err
	}
	if err := recordEvent(ctx, tx, actor.ID, record); err != nil {
		return Record{}, err
	}
	if err := auditAdmin(ctx, tx, actor, record, "application.create", requestID, now); err != nil {
		return Record{}, err
	}
	items := []Record{record}
	if err := attachJobs(ctx, tx, items, now); err != nil {
		return Record{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Record{}, err
	}
	return items[0], nil
}

// Update requires the revision read by the editing device and records the resulting state.
func (s *Store) Update(ctx context.Context, scope Scope, id string, input Input, requestID string, now time.Time) (Record, error) {
	if err := validate(input); err != nil {
		return Record{}, err
	}
	if input.Version < 1 {
		return Record{}, identity.ErrInvalidInput
	}
	tx, actor, err := s.authorized(ctx, scope, now)
	if err != nil {
		return Record{}, err
	}
	defer rollback(tx)
	record, err := readRecord(ctx, tx, scope, actor, id, true, now)
	if err != nil {
		return Record{}, err
	}
	if input.JobID != "" && input.JobID != record.JobID {
		return Record{}, identity.ErrInvalidInput
	}
	if input.Version != record.Version {
		return Record{}, ErrVersionConflict
	}
	_, err = tx.Exec(ctx, "UPDATE applications SET status=$2,notes=$3,next_action=$4,next_action_at=$5,version=version+1,updated_at=$6 WHERE id=$1", record.ID, input.Status, input.Notes, input.NextAction, input.NextActionAt, now)
	if err != nil {
		return Record{}, err
	}
	record, err = readRecord(ctx, tx, scope, actor, id, false, now)
	if err != nil {
		return Record{}, err
	}
	if err := recordEvent(ctx, tx, actor.ID, record); err != nil {
		return Record{}, err
	}
	if err := auditAdmin(ctx, tx, actor, record, "application.update", requestID, now); err != nil {
		return Record{}, err
	}
	items := []Record{record}
	if err := attachJobs(ctx, tx, items, now); err != nil {
		return Record{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Record{}, err
	}
	return items[0], nil
}

// Delete removes an explicitly versioned record and its private event history.
func (s *Store) Delete(ctx context.Context, scope Scope, id string, version int64, requestID string, now time.Time) error {
	if version < 1 {
		return identity.ErrInvalidInput
	}
	tx, actor, err := s.authorized(ctx, scope, now)
	if err != nil {
		return err
	}
	defer rollback(tx)
	record, err := readRecord(ctx, tx, scope, actor, id, true, now)
	if err != nil {
		return err
	}
	if version != record.Version {
		return ErrVersionConflict
	}
	if err := auditAdmin(ctx, tx, actor, record, "application.delete", requestID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM applications WHERE id=$1", record.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
