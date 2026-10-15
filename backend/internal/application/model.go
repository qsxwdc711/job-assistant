// Package application owns private favorites, application progress and optimistic edits.
package application

import (
	"errors"
	"time"

	"jobassistant/backend/internal/catalog"
)

var (
	// ErrVersionConflict indicates that another device changed the record before this edit.
	ErrVersionConflict = errors.New("application version changed")
	// ErrAlreadyExists indicates an existing main record for this owner and job.
	ErrAlreadyExists = errors.New("application already exists")
)

// Scope is constructed by the server from the session and an authorized administrator route.
type Scope struct {
	ActorID      string
	OwnerID      string
	RequireAdmin bool
}

// Input is a complete private edit, with a mandatory expected version on existing records.
type Input struct {
	JobID        string     `json:"job_id"`
	Status       string     `json:"status"`
	Notes        string     `json:"notes"`
	NextAction   string     `json:"next_action"`
	NextActionAt *time.Time `json:"next_action_at"`
	Version      int64      `json:"version"`
}

// Record preserves personal progress independently of a public job's current status.
type Record struct {
	ID              string      `json:"id"`
	OwnerUserID     string      `json:"owner_user_id"`
	OwnerUsername   string      `json:"owner_username"`
	JobID           string      `json:"job_id"`
	ResumeVersionID *string     `json:"resume_version_id"`
	Status          string      `json:"status"`
	Notes           string      `json:"notes"`
	NextAction      string      `json:"next_action"`
	NextActionAt    *time.Time  `json:"next_action_at"`
	Version         int64       `json:"version"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
	Job             catalog.Job `json:"job"`
}

// Event stores the actual state after an edit, including its revision and actor.
type Event struct {
	ID           string     `json:"id"`
	ActorUserID  string     `json:"actor_user_id"`
	Status       string     `json:"status"`
	Notes        string     `json:"notes"`
	NextAction   string     `json:"next_action"`
	NextActionAt *time.Time `json:"next_action_at"`
	Version      int64      `json:"version"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Page is a bounded list of private application records.
type Page struct {
	Items  []Record `json:"items"`
	Total  int      `json:"total"`
	Limit  int      `json:"limit"`
	Offset int      `json:"offset"`
}

// Favorite contains a current public job and the owner's saved time.
type Favorite struct {
	Job       catalog.Job `json:"job"`
	CreatedAt time.Time   `json:"created_at"`
}
