package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,39}$`)

// Options configures the service; PasswordCost may be lowered in isolated tests.
// Entrypoints always use the default cost of 12.
type Options struct {
	Now          func() time.Time
	PasswordCost int
}

// Service applies input and permission rules before persistence.
type Service struct {
	store        *Store
	now          func() time.Time
	passwordCost int
	dummyHash    []byte
}

// NewService creates an identity service with a real password verifier.
func NewService(store *Store, options Options) (*Service, error) {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.PasswordCost == 0 {
		options.PasswordCost = 12
	}
	if options.PasswordCost < bcrypt.MinCost || options.PasswordCost > 14 {
		return nil, fmt.Errorf("%w: unsupported password cost", ErrInvalidInput)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("unusable-dummy-credential"), options.PasswordCost)
	if err != nil {
		return nil, fmt.Errorf("create timing defense: %w", err)
	}
	return &Service{store: store, now: options.Now, passwordCost: options.PasswordCost, dummyHash: hash}, nil
}

// Now returns the service clock in UTC.
func (s *Service) Now() time.Time { return s.now().UTC() }

// CreateUserInput describes an administrator-created account.
type CreateUserInput struct {
	Username     string     `json:"username"`
	Password     string     `json:"password"`
	Role         Role       `json:"role"`
	VIPExpiresAt *time.Time `json:"vip_expires_at"`
}

func normalizeUsername(username string) (string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(username) {
		return "", fmt.Errorf("%w: username must contain 3-40 ASCII letters, numbers, dot, underscore or hyphen", ErrInvalidInput)
	}
	return username, nil
}

func validateRole(role Role, expiry *time.Time, now time.Time) error {
	switch role {
	case RoleStandard, RoleAdmin:
		if expiry != nil {
			return fmt.Errorf("%w: only VIP can have an expiry", ErrInvalidInput)
		}
	case RoleVIP:
		if expiry != nil && !expiry.After(now) {
			return fmt.Errorf("%w: VIP expiry must be in the future", ErrInvalidInput)
		}
	default:
		return fmt.Errorf("%w: unknown role", ErrInvalidInput)
	}
	return nil
}

func (s *Service) prepare(input CreateUserInput) (CreateUserInput, string, error) {
	username, err := normalizeUsername(input.Username)
	if err != nil {
		return input, "", err
	}
	input.Username = username
	if input.Role == "" {
		input.Role = RoleStandard
	}
	if err := validateRole(input.Role, input.VIPExpiresAt, s.Now()); err != nil {
		return input, "", err
	}
	if len(input.Password) < 12 || len(input.Password) > 72 {
		return input, "", fmt.Errorf("%w: password must be 12-72 UTF-8 bytes", ErrInvalidInput)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), s.passwordCost)
	if err != nil {
		return input, "", fmt.Errorf("hash password: %w", err)
	}
	return input, string(hash), nil
}

// Bootstrap creates the first administrator under a database-wide lock.
func (s *Service) Bootstrap(ctx context.Context, username, password string) (User, error) {
	input, hash, err := s.prepare(CreateUserInput{Username: username, Password: password, Role: RoleAdmin})
	if err != nil {
		return User{}, err
	}
	return s.store.bootstrap(ctx, input, hash, s.Now())
}

// CreateUser creates an account and its audit record in one transaction.
func (s *Service) CreateUser(ctx context.Context, actor User, input CreateUserInput, requestID string) (User, error) {
	if err := Authorize(actor, ManageUsers, "", s.Now()); err != nil {
		return User{}, err
	}
	input, hash, err := s.prepare(input)
	if err != nil {
		return User{}, err
	}
	return s.store.createUser(ctx, actor.ID, input, hash, requestID, s.Now())
}

// ChangeRole grants or revokes VIP/admin capabilities for subsequent requests.
func (s *Service) ChangeRole(ctx context.Context, actor User, targetID string, role Role, expiry *time.Time, requestID string) (User, error) {
	if err := Authorize(actor, ManageUsers, "", s.Now()); err != nil {
		return User{}, err
	}
	if err := validateRole(role, expiry, s.Now()); err != nil {
		return User{}, err
	}
	return s.store.updateUser(ctx, actor.ID, targetID, &role, expiry, nil, requestID, s.Now())
}

// SetDisabled updates account availability and revokes all sessions if disabled.
func (s *Service) SetDisabled(ctx context.Context, actor User, targetID string, disabled bool, requestID string) (User, error) {
	if err := Authorize(actor, ManageUsers, "", s.Now()); err != nil {
		return User{}, err
	}
	return s.store.updateUser(ctx, actor.ID, targetID, nil, nil, &disabled, requestID, s.Now())
}

// LoginResult returns the opaque cookie token separately from public state.
type LoginResult struct {
	Principal Principal
	Token     string
	ExpiresAt time.Time
}

func tokenHash(token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, ErrUnauthenticated
	}
	sum := sha256.Sum256([]byte(token))
	return sum[:], nil
}

// Login uses a generic failure for unknown, disabled, and incorrect credentials.
func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	normalized, err := normalizeUsername(username)
	if err != nil || len(password) > 72 || len(password) == 0 {
		return LoginResult{}, ErrUnauthenticated
	}
	u, hash, err := s.store.credential(ctx, normalized)
	if err != nil {
		if err == ErrNotFound {
			// The result is irrelevant for an unknown account; perform the same
			// expensive comparison before returning the generic login failure.
			_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
			return LoginResult{}, ErrUnauthenticated
		}
		return LoginResult{}, err
	}
	passwordErr := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if passwordErr != nil || u.Disabled {
		return LoginResult{}, ErrUnauthenticated
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return LoginResult{}, fmt.Errorf("create session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest, err := tokenHash(token)
	if err != nil {
		return LoginResult{}, err
	}
	now := s.Now()
	expires := now.Add(7 * 24 * time.Hour)
	p, err := s.store.createSession(ctx, u.ID, digest, now, expires)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Principal: p, Token: token, ExpiresAt: expires}, nil
}

// Authenticate reloads the account role instead of trusting session snapshots.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	hash, err := tokenHash(token)
	if err != nil {
		return Principal{}, err
	}
	return s.store.authenticate(ctx, hash, s.Now())
}

// Sessions returns the current user's live device-session metadata.
func (s *Service) Sessions(ctx context.Context, p Principal) ([]SessionInfo, error) {
	if err := Authorize(p.User, ManageSessions, p.User.ID, s.Now()); err != nil {
		return nil, err
	}
	return s.store.sessions(ctx, p.User.ID, p.SessionID, s.Now())
}

// RevokeSession removes only an owned session unless the actor is an admin.
func (s *Service) RevokeSession(ctx context.Context, actor User, targetID, requestID string) error {
	return s.store.revokeSession(ctx, actor.ID, targetID, requestID, s.Now())
}

// Users lists accounts for an administrator.
func (s *Service) Users(ctx context.Context, actor User, limit, offset int) ([]User, error) {
	if err := Authorize(actor, ManageUsers, "", s.Now()); err != nil {
		return nil, err
	}
	return s.store.users(ctx, actor.ID, limit, offset, s.Now())
}

// Audit lists sensitive-operation records for an administrator.
func (s *Service) Audit(ctx context.Context, actor User, limit, offset int) ([]AuditEvent, error) {
	if err := Authorize(actor, ReadAudit, "", s.Now()); err != nil {
		return nil, err
	}
	return s.store.audit(ctx, actor.ID, limit, offset, s.Now())
}
