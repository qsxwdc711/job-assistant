// Package identity manages accounts, sessions, and authorization.
package identity

import (
	"errors"
	"time"
)

// Role is an account's assigned role.
type Role string

const (
	RoleStandard Role = "standard"
	RoleVIP      Role = "vip"
	RoleAdmin    Role = "admin"
)

// Action is a capability checked before a business operation.
type Action string

const (
	BrowseJobs            Action = "jobs.browse"
	ManageApplications    Action = "applications.manage"
	ManageSubscriptions   Action = "subscriptions.manage"
	ManageSessions        Action = "sessions.manage"
	UploadResume          Action = "resumes.upload"
	ReadResume            Action = "resumes.read"
	DeleteResume          Action = "resumes.delete"
	CreateRecommendations Action = "recommendations.create"
	ReadRecommendations   Action = "recommendations.read"
	ManageUsers           Action = "users.manage"
	ManageCatalog         Action = "catalog.manage"
	ReadAudit             Action = "audit.read"
)

var actions = []Action{
	BrowseJobs, ManageApplications, ManageSubscriptions, ManageSessions,
	UploadResume, ReadResume, DeleteResume, CreateRecommendations,
	ReadRecommendations, ManageUsers, ManageCatalog, ReadAudit,
}

var (
	ErrUnauthenticated     = errors.New("authentication required")
	ErrForbidden           = errors.New("permission denied")
	ErrNotFound            = errors.New("resource not found")
	ErrConflict            = errors.New("username already exists")
	ErrAdministratorExists = errors.New("administrator already exists")
	ErrLastAdministrator   = errors.New("cannot remove the last active administrator")
	ErrInvalidInput        = errors.New("invalid input")
)

// User contains public account state; password hashes are never included.
type User struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	Role         Role       `json:"role"`
	VIPExpiresAt *time.Time `json:"vip_expires_at"`
	Disabled     bool       `json:"disabled"`
	AuthVersion  int64      `json:"auth_version"`
	CreatedAt    time.Time  `json:"created_at"`
}

// EffectiveRole accounts for expiration and invalid or disabled accounts.
func (u User) EffectiveRole(now time.Time) Role {
	if u.Disabled {
		return ""
	}
	switch u.Role {
	case RoleAdmin, RoleStandard:
		return u.Role
	case RoleVIP:
		if u.VIPExpiresAt == nil || u.VIPExpiresAt.After(now) {
			return RoleVIP
		}
		return RoleStandard
	default:
		return ""
	}
}

// Authorize checks capability first, then ownership. An empty owner is used
// for checking capabilities before a resource is created.
func Authorize(u User, action Action, ownerID string, now time.Time) error {
	if u.ID == "" || u.Disabled || u.EffectiveRole(now) == "" {
		return ErrUnauthenticated
	}
	known, allowed := false, false
	role := u.EffectiveRole(now)
	for _, candidate := range actions {
		if action == candidate {
			known = true
			break
		}
	}
	if !known {
		return ErrForbidden
	}
	switch action {
	case BrowseJobs, ManageApplications, ManageSubscriptions, ManageSessions, DeleteResume:
		allowed = true
	case UploadResume, ReadResume, CreateRecommendations, ReadRecommendations:
		allowed = role == RoleVIP || role == RoleAdmin
	case ManageUsers, ManageCatalog, ReadAudit:
		allowed = role == RoleAdmin
	}
	if !allowed {
		return ErrForbidden
	}
	if ownerID != "" && ownerID != u.ID && role != RoleAdmin && action != BrowseJobs {
		return ErrNotFound
	}
	return nil
}

// Capabilities returns the current capability map for client display.
func Capabilities(u User, now time.Time) map[Action]bool {
	result := make(map[Action]bool, len(actions))
	for _, action := range actions {
		result[action] = Authorize(u, action, "", now) == nil
	}
	return result
}
