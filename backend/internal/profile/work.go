package profile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/task"
)

// Work is transient worker input; none of its private text is written to queue records or logs.
type Work struct {
	Filename string
	Data     []byte
	Text     string
	Names    []string
	Done     bool
}

func (s *Store) taskResume(ctx context.Context, tx pgx.Tx, j task.Job, now time.Time) (privateResume, error) {
	if err := task.CheckAccess(ctx, tx, j, identity.UploadResume, now); err != nil {
		return privateResume{}, err
	}
	r, err := loadResume(ctx, tx, Scope{ActorID: j.ActorID, OwnerID: j.OwnerID, Admin: j.ActorID != j.OwnerID}, j.ResourceID, true)
	if errors.Is(err, identity.ErrNotFound) {
		return r, task.ErrCancelled
	}
	if err != nil {
		return r, err
	}
	if r.TaskID == nil || *r.TaskID != j.ID {
		return r, task.ErrCancelled
	}
	if err = task.CheckLease(ctx, tx, j, now); err != nil {
		return r, err
	}
	return r, nil
}

// Prepare checks permission immediately before local extraction or an external parse call.
func (s *Store) Prepare(ctx context.Context, j task.Job, now time.Time) (Work, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Work{}, err
	}
	defer task.Rollback(tx)
	r, err := s.taskResume(ctx, tx, j, now)
	if err != nil {
		return Work{}, err
	}
	done := (j.Kind == "extract" && r.Status == "review" || j.Kind == "parse" && r.Status == "draft") && r.Revision > j.Revision
	if !done && r.Revision != j.Revision {
		return Work{}, task.ErrCancelled
	}
	if j.Kind == "parse" && !r.approved {
		return Work{}, task.ErrCancelled
	}
	work := Work{Filename: r.Filename, Text: r.text, Names: r.names, Done: done}
	if !done && j.Kind == "extract" {
		work.Data, err = s.vault.read(r.key)
		if err != nil {
			return Work{}, ErrInvalidFile
		}
		sum := sha256.Sum256(work.Data)
		if hex.EncodeToString(sum[:]) != r.hash || len(work.Data) != r.SizeBytes {
			return Work{}, ErrInvalidFile
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Work{}, err
	}
	return work, nil
}

// Complete persists results only if the exact lease, account versions and resume revision remain valid.
func (s *Store) Complete(ctx context.Context, j task.Job, text string, draft Candidate, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer task.Rollback(tx)
	r, err := s.taskResume(ctx, tx, j, now)
	if err != nil {
		return err
	}
	if r.Revision != j.Revision {
		return task.ErrCancelled
	}
	status := "review"
	if j.Kind == "parse" {
		status = "draft"
		text = r.text
		if err = ValidateCandidate(draft, text, true); err != nil {
			return err
		}
	}
	data, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE resumes SET redacted_text=$2,draft=$3,status=$4,error_code='',revision=revision+1,updated_at=$5 WHERE id=$1`, r.ID, text, data, status, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Fail preserves existing local text while reporting a sanitized terminal processing error.
func (s *Store) Fail(ctx context.Context, j task.Job, code string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer task.Rollback(tx)
	r, err := s.taskResume(ctx, tx, j, now)
	if err != nil {
		return err
	}
	if r.Revision != j.Revision {
		return task.ErrCancelled
	}
	status := "failed"
	if r.text != "" {
		status = "review"
	}
	if _, err = tx.Exec(ctx, `UPDATE resumes SET status=$2,error_code=$3,updated_at=$4 WHERE id=$1`, r.ID, status, code, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
