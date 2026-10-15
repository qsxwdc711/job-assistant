package profile

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/task"
)

// UpdatePreview lets the owner remove remaining sensitive details before granting cloud consent.
func (s *Store) UpdatePreview(ctx context.Context, scope Scope, id string, revision int64, text, requestID string, now time.Time) error {
	if len(text) > MaxTextBytes || len(strings.TrimSpace(text)) < 8 {
		return identity.ErrInvalidInput
	}
	tx, _, err := s.authorized(ctx, scope, identity.UploadResume, now)
	if err != nil {
		return err
	}
	defer task.Rollback(tx)
	r, err := loadResume(ctx, tx, scope, id, true)
	if err != nil {
		return err
	}
	busy, err := activeTask(ctx, tx, r)
	if err != nil {
		return err
	}
	if busy || r.Revision != revision {
		return ErrStale
	}
	if _, err = tx.Exec(ctx, `UPDATE resumes SET redacted_text=$2,draft='{}',cloud_approved=false,status='review',error_code='',revision=revision+1,updated_at=$3 WHERE id=$1`, id, Redact(text, r.names), now); err != nil {
		return err
	}
	if err = identity.AppendAudit(ctx, tx, scope.ActorID, "resume.preview_update", id, requestID, nil, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func activeTask(ctx context.Context, tx pgx.Tx, r privateResume) (bool, error) {
	var active bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM private_tasks WHERE id=$1 AND state IN ('queued','running'))`, r.TaskID).Scan(&active)
	return active, err
}

// StartParse records explicit external-processing consent and queues only locally redacted text.
func (s *Store) StartParse(ctx context.Context, scope Scope, id string, revision int64, consent, configured bool, requestID string, now time.Time) (task.Job, error) {
	tx, actor, err := s.authorized(ctx, scope, identity.UploadResume, now)
	if err != nil {
		return task.Job{}, err
	}
	defer task.Rollback(tx)
	r, err := loadResume(ctx, tx, scope, id, true)
	if err != nil {
		return task.Job{}, err
	}
	if !consent {
		return task.Job{}, identity.ErrInvalidInput
	}
	if !configured {
		return task.Job{}, ErrUnconfigured
	}
	if r.text == "" {
		return task.Job{}, identity.ErrInvalidInput
	}
	active, err := activeTask(ctx, tx, r)
	if err != nil {
		return task.Job{}, err
	}
	if active || revision != r.Revision {
		return task.Job{}, ErrStale
	}
	ownerUser, err := identity.CheckAccess(ctx, tx, r.OwnerID, identity.ManageApplications, "", now)
	if err != nil {
		return task.Job{}, err
	}
	r.Revision++
	j, err := task.Enqueue(ctx, tx, actor, ownerUser, "parse", r.ID, r.Revision, now)
	if err != nil {
		return task.Job{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE resumes SET status='parsing',cloud_approved=true,revision=$2,last_task_id=$3,error_code='',updated_at=$4 WHERE id=$1`, r.ID, r.Revision, j.ID, now); err != nil {
		return task.Job{}, err
	}
	if err = identity.AppendAudit(ctx, tx, actor.ID, "resume.parse", r.ID, requestID, map[string]string{"owner_user_id": r.OwnerID}, now); err != nil {
		return task.Job{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return task.Job{}, err
	}
	r.Status = "parsing"
	r.TaskID = &j.ID
	return j, nil
}

// Confirm creates an immutable version; later parsing cannot silently overwrite confirmed facts.
func (s *Store) Confirm(ctx context.Context, scope Scope, id string, revision int64, candidate Candidate, cloudApproved bool, requestID string, now time.Time) (Version, error) {
	if err := ValidateCandidate(candidate, "", false); err != nil {
		return Version{}, err
	}
	candidate.Evidence = nil
	tx, actor, err := s.authorized(ctx, scope, identity.UploadResume, now)
	if err != nil {
		return Version{}, err
	}
	defer task.Rollback(tx)
	r, err := loadResume(ctx, tx, scope, id, true)
	if err != nil {
		return Version{}, err
	}
	active, err := activeTask(ctx, tx, r)
	if err != nil {
		return Version{}, err
	}
	if active || revision != r.Revision {
		return Version{}, ErrStale
	}
	if r.text == "" {
		return Version{}, identity.ErrInvalidInput
	}
	data, err := json.Marshal(candidate)
	if err != nil {
		return Version{}, err
	}
	text := Redact(r.text+"\n已确认求职资料：\n"+string(data), r.names)
	v := Version{ResumeID: r.ID, Candidate: candidate, CloudApproved: cloudApproved, Current: true, CreatedAt: now}
	if err = tx.QueryRow(ctx, `INSERT INTO profile_versions(owner_user_id,resume_id,candidate,redacted_text,cloud_approved,created_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING id::text`, r.OwnerID, r.ID, data, text, cloudApproved, now).Scan(&v.ID); err != nil {
		return Version{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO current_profiles(owner_user_id,profile_version_id,updated_at) VALUES($1,$2,$3) ON CONFLICT(owner_user_id) DO UPDATE SET profile_version_id=$2,updated_at=$3`, r.OwnerID, v.ID, now); err != nil {
		return Version{}, err
	}
	if err = stalePrevious(ctx, tx, r.OwnerID, v.ID, now); err != nil {
		return Version{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE resumes SET status='confirmed',revision=revision+1,draft=$2,error_code='',updated_at=$3 WHERE id=$1`, r.ID, data, now); err != nil {
		return Version{}, err
	}
	if err = identity.AppendAudit(ctx, tx, actor.ID, "profile.confirm", v.ID, requestID, map[string]string{"owner_user_id": r.OwnerID, "resume_id": r.ID}, now); err != nil {
		return Version{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Version{}, err
	}
	return v, nil
}

func stalePrevious(ctx context.Context, tx pgx.Tx, ownerID, current string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE recommendations SET semantic_status='stale',updated_at=$3 WHERE owner_user_id=$1 AND profile_version_id<>$2`, ownerID, current, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE private_tasks SET state='cancelled',error_code='profile_changed',lease_token='',lease_until=NULL,updated_at=$3 WHERE owner_user_id=$1 AND kind='recommend' AND state IN ('queued','running') AND resource_id IN (SELECT id FROM recommendations WHERE owner_user_id=$1 AND profile_version_id<>$2)`, ownerID, current, now)
	return err
}

// Activate selects an existing confirmed version and invalidates work for the previous selection.
func (s *Store) Activate(ctx context.Context, scope Scope, id, versionID, requestID string, now time.Time) error {
	tx, actor, err := s.authorized(ctx, scope, identity.UploadResume, now)
	if err != nil {
		return err
	}
	defer task.Rollback(tx)
	r, err := loadResume(ctx, tx, scope, id, false)
	if err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM profile_versions WHERE id=$1 AND resume_id=$2 AND owner_user_id=$3)`, versionID, id, r.OwnerID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return identity.ErrNotFound
	}
	if _, err = tx.Exec(ctx, `INSERT INTO current_profiles(owner_user_id,profile_version_id,updated_at) VALUES($1,$2,$3) ON CONFLICT(owner_user_id) DO UPDATE SET profile_version_id=$2,updated_at=$3`, r.OwnerID, versionID, now); err != nil {
		return err
	}
	if err = stalePrevious(ctx, tx, r.OwnerID, versionID, now); err != nil {
		return err
	}
	if err = identity.AppendAudit(ctx, tx, actor.ID, "profile.activate", versionID, requestID, map[string]string{"owner_user_id": r.OwnerID}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Delete retains only a tombstone, cancels pending work and queues reliable physical file deletion.
func (s *Store) Delete(ctx context.Context, scope Scope, id, requestID string, now time.Time) error {
	tx, actor, err := s.authorized(ctx, scope, identity.DeleteResume, now)
	if err != nil {
		return err
	}
	defer task.Rollback(tx)
	r, err := loadResume(ctx, tx, scope, id, true)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE private_tasks SET state='cancelled',error_code='resume_deleted',lease_token='',lease_until=NULL,updated_at=$2 WHERE state IN ('queued','running') AND ((kind IN ('extract','parse') AND resource_id=$1) OR (kind='recommend' AND resource_id IN (SELECT m.id FROM recommendations m JOIN profile_versions v ON v.id=m.profile_version_id WHERE v.resume_id=$1)))`, id, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM profile_versions WHERE resume_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE resumes SET filename='',redacted_text='',draft='{}',sensitive_names='[]',cloud_approved=false,status='deleted',deleted_at=$2,updated_at=$2,revision=revision+1 WHERE id=$1`, id, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO private_file_deletions(storage_key,created_at) VALUES($1,$2) ON CONFLICT DO NOTHING`, r.key, now); err != nil {
		return err
	}
	if err = identity.AppendAudit(ctx, tx, actor.ID, "resume.delete", id, requestID, map[string]string{"owner_user_id": r.OwnerID}, now); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if err = s.CleanupOne(ctx); err != nil {
		slog.Warn("private file deletion remains queued")
	}
	return nil
}

// CleanupOne can run without the owner's current privileges because deletion was already authorized.
func (s *Store) CleanupOne(ctx context.Context) error {
	var key string
	err := s.pool.QueryRow(ctx, `SELECT storage_key FROM private_file_deletions ORDER BY created_at LIMIT 1`).Scan(&key)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if err = s.vault.remove(key); err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM private_file_deletions WHERE storage_key=$1`, key)
	return err
}

// Versions exposes confirmed facts only through current VIP/admin authorization.
func (s *Store) Versions(ctx context.Context, scope Scope, requestID string, now time.Time) ([]Version, error) {
	tx, _, err := s.authorized(ctx, scope, identity.ReadResume, now)
	if err != nil {
		return nil, err
	}
	defer task.Rollback(tx)
	rows, err := tx.Query(ctx, `SELECT v.id::text,v.resume_id::text,v.candidate,v.cloud_approved,COALESCE(c.profile_version_id=v.id,false),v.created_at FROM profile_versions v LEFT JOIN current_profiles c ON c.owner_user_id=v.owner_user_id JOIN resumes r ON r.id=v.resume_id WHERE r.deleted_at IS NULL AND ($1='' OR v.owner_user_id::text=$1) ORDER BY COALESCE(c.profile_version_id=v.id,false) DESC,v.created_at DESC,v.id LIMIT 100`, owner(scope))
	if err != nil {
		return nil, err
	}
	items := []Version{}
	for rows.Next() {
		var v Version
		var data []byte
		if err = rows.Scan(&v.ID, &v.ResumeID, &data, &v.CloudApproved, &v.Current, &v.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(data, &v.Candidate); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if scope.Admin {
		if err = identity.AppendAudit(ctx, tx, scope.ActorID, "profile.list", owner(scope), requestID, map[string]bool{"admin": true}, now); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}
