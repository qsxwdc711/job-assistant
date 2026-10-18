// Package notification owns per-user subscriptions and public-only delivery outboxes.
package notification

import (
	"errors"
	"time"
)

var (
	// ErrUnconfigured prevents storing or using a channel without an encryption key.
	ErrUnconfigured = errors.New("notification encryption is not configured")
	// ErrVersionConflict rejects stale edits from another device.
	ErrVersionConflict = errors.New("notification configuration changed")
)

// Scope separates personal access from explicit administrator access to an owner.
type Scope struct {
	ActorID, OwnerID string
	Admin            bool
}

// Subscription stores one account's filters and delivery mode.
type Subscription struct {
	FiltersQuery string `json:"filters_query"`
	Mode         string `json:"mode"`
	Enabled      bool   `json:"enabled"`
	Revision     int64  `json:"revision"`
}

// ChannelView exposes configuration metadata without credentials or ciphertext.
type ChannelView struct {
	Configured    bool  `json:"configured"`
	Enabled       bool  `json:"enabled"`
	HasSecret     bool  `json:"has_secret"`
	Revision      int64 `json:"revision"`
	KeyConfigured bool  `json:"key_configured"`
	SendEnabled   bool  `json:"send_enabled"`
}

// ChannelInput changes write-only credentials; nil pointers preserve existing values.
type ChannelInput struct {
	Webhook  *string `json:"webhook"`
	Secret   *string `json:"secret"`
	Enabled  bool    `json:"enabled"`
	Revision int64   `json:"revision"`
}

// ChannelSecret is encrypted at rest and consumed only by the outbound sender.
type ChannelSecret struct {
	Webhook string `json:"webhook"`
	Secret  string `json:"secret"`
}

// Item is the only job projection permitted in group messages and notification snapshots.
type Item struct {
	JobID             string   `json:"job_id"`
	JobVersion        int64    `json:"job_version"`
	Company           string   `json:"company"`
	Title             string   `json:"title"`
	VariantKeys       []string `json:"variant_keys"`
	Locations         []string `json:"locations"`
	Deadline          string   `json:"deadline"`
	NeedsConfirmation bool     `json:"needs_confirmation"`
	OfficialURL       string   `json:"official_url"`
	DetailsURL        string   `json:"details_url"`
	Priority          int      `json:"priority"`
}

// Batch is an owner-scoped station record containing public job projections.
type Batch struct {
	ID        string            `json:"id"`
	OwnerID   string            `json:"owner_user_id"`
	Kind      string            `json:"kind"`
	Mode      string            `json:"mode"`
	Cutoff    time.Time         `json:"cutoff"`
	State     string            `json:"state"`
	ErrorCode string            `json:"error_code"`
	Attempts  int               `json:"attempts"`
	Items     []Item            `json:"items"`
	Sources   map[string]string `json:"sources"`
	CreatedAt time.Time         `json:"created_at"`
}
