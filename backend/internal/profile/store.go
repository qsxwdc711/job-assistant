package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/task"
)

// Resume is metadata available to its owner even after VIP expires.
type Resume struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"owner_user_id"`
	Filename  string    `json:"filename"`
	SizeBytes int       `json:"size_bytes"`
	Status    string    `json:"status"`
	ErrorCode string    `json:"error_code"`
	Revision  int64     `json:"revision"`
	TaskID    *string   `json:"task_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Version is an immutable set of user-confirmed facts.
type Version struct {
	ID            string    `json:"id"`
	ResumeID      string    `json:"resume_id"`
	Candidate     Candidate `json:"candidate"`
	CloudApproved bool      `json:"cloud_approved"`
	Current       bool      `json:"current"`
	CreatedAt     time.Time `json:"created_at"`
}

// Detail requires VIP/admin access and contains only locally redacted text.
type Detail struct {
	Resume       Resume    `json:"resume"`
	RedactedText string    `json:"redacted_text"`
	Draft        Candidate `json:"draft"`
	Versions     []Version `json:"versions"`
}

type privateResume struct {
	Resume
	key      string
	hash     string
	names    []string
	text     string
	draft    Candidate
	approved bool
	deleted  *time.Time
}

// Store owns authorized resume transactions and a caller-owned private vault.
type Store struct {
	pool  *pgxpool.Pool
	vault *Vault
}

// NewStore connects private files to their transactional metadata.
func NewStore(pool *pgxpool.Pool, vault *Vault) *Store { return &Store{pool: pool, vault: vault} }

const resumeColumns = `r.id::text,r.owner_user_id::text,r.filename,r.size_bytes,r.status,r.error_code,r.revision,r.last_task_id::text,r.created_at,r.updated_at`

type scanner interface{ Scan(...any) error }

func scanResume(row scanner) (Resume, error) {
	var r Resume
	err := row.Scan(&r.ID, &r.OwnerID, &r.Filename, &r.SizeBytes, &r.Status, &r.ErrorCode, &r.Revision, &r.TaskID, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, identity.ErrNotFound
	}
	return r, err
}

func (s *Store) authorized(ctx context.Context, scope Scope, action identity.Action, now time.Time) (pgx.Tx, identity.User, error) {
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
	if action == identity.UploadResume {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,73003))`, owner(scope)); err != nil {
			task.Rollback(tx)
			return nil, identity.User{}, err
		}
	}
	return tx, actor, nil
}
func owner(scope Scope) string {
	if scope.Admin {
		return scope.OwnerID
	}
	return scope.ActorID
}

func loadResume(ctx context.Context, tx pgx.Tx, scope Scope, id string, lock bool) (privateResume, error) {
	clause := " FOR SHARE"
	if lock {
		clause = " FOR UPDATE"
	}
	var r privateResume
	var names, draft []byte
	err := tx.QueryRow(ctx, `SELECT `+resumeColumns+`,r.storage_key,r.content_sha256,r.sensitive_names,r.redacted_text,r.draft,r.cloud_approved,r.deleted_at FROM resumes r WHERE r.id=$1 AND (r.owner_user_id::text=$2 OR $3) AND ($4='' OR r.owner_user_id::text=$4)`+clause, id, scope.ActorID, scope.Admin, owner(scope)).Scan(&r.ID, &r.OwnerID, &r.Filename, &r.SizeBytes, &r.Status, &r.ErrorCode, &r.Revision, &r.TaskID, &r.CreatedAt, &r.UpdatedAt, &r.key, &r.hash, &names, &r.text, &draft, &r.approved, &r.deleted)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && r.deleted != nil {
		return r, identity.ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(names, &r.names); err != nil {
		return r, err
	}
	if err = json.Unmarshal(draft, &r.draft); err != nil {
		return r, err
	}
	return r, nil
}

// List exposes only bounded file metadata; administrator reads are audited without filenames.
func (s *Store) List(ctx context.Context, scope Scope, limit, offset int, requestID string, now time.Time) ([]Resume, error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return nil, identity.ErrInvalidInput
	}
	tx, _, err := s.authorized(ctx, scope, identity.DeleteResume, now)
	if err != nil {
		return nil, err
	}
	defer task.Rollback(tx)
	rows, err := tx.Query(ctx, `SELECT `+resumeColumns+` FROM resumes r WHERE r.deleted_at IS NULL AND ($1='' OR r.owner_user_id::text=$1) ORDER BY r.created_at DESC,r.id LIMIT $2 OFFSET $3`, owner(scope), limit, offset)
	if err != nil {
		return nil, err
	}
	items := []Resume{}
	for rows.Next() {
		item, err := scanResume(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if scope.Admin {
		if err = identity.AppendAudit(ctx, tx, scope.ActorID, "resume.list", owner(scope), requestID, map[string]int{"count": len(items)}, now); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// Upload stores a validated original and queues local extraction in one authorized transaction.
func (s *Store) Upload(ctx context.Context, scope Scope, filename string, data []byte, names []string, requestID string, now time.Time) (Resume, error) {
	if owner(scope) == "" || len(names) > 10 || len(filename) > 240 {
		return Resume{}, identity.ErrInvalidInput
	}
	for _, name := range names {
		if len(name) > 120 {
			return Resume{}, identity.ErrInvalidInput
		}
	}
	tx, actor, err := s.authorized(ctx, scope, identity.UploadResume, now)
	if err != nil {
		return Resume{}, err
	}
	defer task.Rollback(tx)
	key, hash, err := s.vault.put(filename, data)
	if err != nil {
		return Resume{}, err
	}
	committed := false
	defer func() {
		if !committed {
			if err := s.vault.remove(key); err != nil {
				slog.Warn("uncommitted resume file cleanup failed")
			}
		}
	}()
	nameData, err := json.Marshal(names)
	if err != nil {
		return Resume{}, err
	}
	r, err := scanResume(tx.QueryRow(ctx, `INSERT INTO resumes(owner_user_id,filename,storage_key,content_sha256,size_bytes,sensitive_names,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7) RETURNING id::text,owner_user_id::text,filename,size_bytes,status,error_code,revision,last_task_id::text,created_at,updated_at`, owner(scope), filepath.Base(strings.ReplaceAll(filename, `\`, "/")), key, hash, len(data), nameData, now))
	if err != nil {
		return Resume{}, err
	}
	ownerUser, err := identity.CheckAccess(ctx, tx, r.OwnerID, identity.ManageApplications, "", now)
	if err != nil {
		return Resume{}, err
	}
	job, err := task.Enqueue(ctx, tx, actor, ownerUser, "extract", r.ID, r.Revision, now)
	if err != nil {
		return Resume{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE resumes SET last_task_id=$2 WHERE id=$1`, r.ID, job.ID); err != nil {
		return Resume{}, err
	}
	r.TaskID = &job.ID
	if err = identity.AppendAudit(ctx, tx, actor.ID, "resume.upload", r.ID, requestID, map[string]any{"owner_user_id": r.OwnerID, "size_bytes": len(data)}, now); err != nil {
		return Resume{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Resume{}, err
	}
	committed = true
	return r, nil
}

// Detail returns redacted text and up to one hundred confirmed versions to a current VIP/admin.
func (s *Store) Detail(ctx context.Context, scope Scope, id, requestID string, now time.Time) (Detail, error) {
	tx, _, err := s.authorized(ctx, scope, identity.ReadResume, now)
	if err != nil {
		return Detail{}, err
	}
	defer task.Rollback(tx)
	r, err := loadResume(ctx, tx, scope, id, false)
	if err != nil {
		return Detail{}, err
	}
	result := Detail{Resume: r.Resume, RedactedText: r.text, Draft: r.draft, Versions: []Version{}}
	rows, err := tx.Query(ctx, `SELECT v.id::text,v.resume_id::text,v.candidate,v.cloud_approved,COALESCE(c.profile_version_id=v.id,false),v.created_at FROM profile_versions v LEFT JOIN current_profiles c ON c.owner_user_id=v.owner_user_id WHERE v.resume_id=$1 ORDER BY COALESCE(c.profile_version_id=v.id,false) DESC,v.created_at DESC,v.id LIMIT 100`, id)
	if err != nil {
		return Detail{}, err
	}
	for rows.Next() {
		var v Version
		var raw []byte
		if err = rows.Scan(&v.ID, &v.ResumeID, &raw, &v.CloudApproved, &v.Current, &v.CreatedAt); err != nil {
			rows.Close()
			return Detail{}, err
		}
		if err = json.Unmarshal(raw, &v.Candidate); err != nil {
			rows.Close()
			return Detail{}, err
		}
		result.Versions = append(result.Versions, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Detail{}, err
	}
	if scope.Admin {
		if err = identity.AppendAudit(ctx, tx, scope.ActorID, "resume.read", id, requestID, map[string]string{"owner_user_id": r.OwnerID}, now); err != nil {
			return Detail{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Detail{}, err
	}
	return result, nil
}

// Download audits an authorized read and returns an original that is never served statically.
func (s *Store) Download(ctx context.Context, scope Scope, id, requestID string, now time.Time) (Resume, []byte, error) {
	tx, _, err := s.authorized(ctx, scope, identity.ReadResume, now)
	if err != nil {
		return Resume{}, nil, err
	}
	defer task.Rollback(tx)
	r, err := loadResume(ctx, tx, scope, id, false)
	if err != nil {
		return Resume{}, nil, err
	}
	data, err := s.vault.read(r.key)
	if err != nil {
		return Resume{}, nil, fmt.Errorf("private file unavailable")
	}
	if err = identity.AppendAudit(ctx, tx, scope.ActorID, "resume.download", id, requestID, map[string]string{"owner_user_id": r.OwnerID}, now); err != nil {
		return Resume{}, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Resume{}, nil, err
	}
	return r.Resume, data, nil
}
