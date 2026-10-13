package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"jobassistant/backend/internal/httpapi"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/migrations"
)

const testPassword = "only-for-isolated-tests"

type fixture struct {
	pool    *pgxpool.Pool
	service *identity.Service
	clock   *atomic.Int64
	admin   identity.User
}

func newStoreFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; real PostgreSQL integration test skipped")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("integration tests require a dedicated database ending in _test")
	}
	ctx := context.Background()
	control, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("open test database")
	}
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		control.Close()
		t.Fatal(err)
	}
	schema := "ja_test_" + hex.EncodeToString(raw)
	if _, err := control.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		control.Close()
		t.Fatalf("create isolated schema: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "5000"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		control.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := control.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("cleanup isolated schema: %v", err)
		}
		control.Close()
	})
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	clock := &atomic.Int64{}
	clock.Store(time.Now().UTC().Truncate(time.Microsecond).UnixNano())
	service, err := identity.NewService(identity.NewStore(pool), identity.Options{PasswordCost: 4, Now: func() time.Time { return time.Unix(0, clock.Load()).UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{pool: pool, service: service, clock: clock}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := newStoreFixture(t)
	u, err := f.service.Bootstrap(context.Background(), "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	f.admin = u
	return f
}

func (f *fixture) user(t *testing.T, name string, role identity.Role, expiry *time.Time) identity.User {
	t.Helper()
	u, err := f.service.CreateUser(context.Background(), f.admin, identity.CreateUserInput{Username: name, Password: testPassword, Role: role, VIPExpiresAt: expiry}, "test-create")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f *fixture) login(t *testing.T, name string) identity.LoginResult {
	t.Helper()
	result, err := f.service.Login(context.Background(), name, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestIdentityMigrationAndBootstrapRace(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()
	if err := migrations.Apply(ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Check(ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, name := range []string{"first_admin", "second_admin"} {
		go func() { _, err := f.service.Bootstrap(ctx, name, testPassword); results <- err }()
	}
	success, refused := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, identity.ErrAdministratorExists) {
			refused++
		} else {
			t.Errorf("unexpected bootstrap error: %v", err)
		}
	}
	if success != 1 || refused != 1 {
		t.Fatalf("bootstrap outcomes: successes=%d refused=%d", success, refused)
	}
	var admins, audits int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE role='admin'").Scan(&admins); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='admin.bootstrap'").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if admins != 1 || audits != 1 {
		t.Fatalf("admins=%d audits=%d", admins, audits)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE schema_migrations SET checksum='changed'"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, f.pool); err == nil {
		t.Fatal("changed migration checksum accepted")
	}
}

func TestIdentityCurrentRoleExpiryAndDisabledSessions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	expiry := f.service.Now().Add(time.Hour)
	vip := f.user(t, "vip_user", identity.RoleVIP, &expiry)
	first := f.login(t, vip.Username)
	second := f.login(t, vip.Username)
	if first.Token == second.Token {
		t.Fatal("session tokens reused")
	}
	var stored []byte
	if err := f.pool.QueryRow(ctx, "SELECT token_hash FROM sessions WHERE id=$1", first.Principal.SessionID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 32 || bytes.Contains(stored, []byte(first.Token)) {
		t.Fatal("invalid or plaintext session persistence")
	}
	f.clock.Store(expiry.UnixNano())
	p, err := f.service.Authenticate(ctx, first.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Authorize(p.User, identity.ReadRecommendations, p.User.ID, f.service.Now()); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("expired VIP retained permission: %v", err)
	}
	if err := identity.Authorize(p.User, identity.DeleteResume, p.User.ID, f.service.Now()); err != nil {
		t.Fatal("expired VIP lost delete control")
	}
	if _, err := f.service.ChangeRole(ctx, f.admin, vip.ID, identity.RoleVIP, nil, "grant"); err != nil {
		t.Fatal(err)
	}
	p, err = f.service.Authenticate(ctx, first.Token)
	if err != nil || p.User.EffectiveRole(f.service.Now()) != identity.RoleVIP {
		t.Fatalf("grant not visible in old session: %v", err)
	}
	if _, err := f.service.ChangeRole(ctx, f.admin, vip.ID, identity.RoleStandard, nil, "revoke"); err != nil {
		t.Fatal(err)
	}
	p, err = f.service.Authenticate(ctx, first.Token)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Capabilities(p.User, f.service.Now())[identity.CreateRecommendations] {
		t.Fatal("revoked VIP old session retained permission")
	}
	if _, err := f.service.SetDisabled(ctx, f.admin, vip.ID, true, "disable"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first.Token, second.Token} {
		if _, err := f.service.Authenticate(ctx, token); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatalf("disabled session accepted: %v", err)
		}
	}
	if _, err := f.service.SetDisabled(ctx, f.admin, vip.ID, false, "enable"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Authenticate(ctx, first.Token); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("reenabling revived revoked session")
	}
	if _, err := f.service.Login(ctx, vip.Username, testPassword); err != nil {
		t.Fatal(err)
	}
}

func TestIdentitySessionOwnershipAndExpiry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.user(t, "user_a", identity.RoleStandard, nil)
	b := f.user(t, "user_b", identity.RoleVIP, nil)
	sa := f.login(t, a.Username)
	sb := f.login(t, b.Username)
	if err := f.service.RevokeSession(ctx, a, sb.Principal.SessionID, "cross-user"); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("cross-user result: %v", err)
	}
	if _, err := f.service.Authenticate(ctx, sb.Token); err != nil {
		t.Fatal("cross-user attempt changed another session")
	}
	if err := f.service.RevokeSession(ctx, f.admin, sb.Principal.SessionID, "admin-revoke"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Authenticate(ctx, sb.Token); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("admin revoke ineffective")
	}
	f.clock.Store(sa.ExpiresAt.UnixNano())
	if _, err := f.service.Authenticate(ctx, sa.Token); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("session valid at exact expiry")
	}
}

func TestIdentityLastAdministratorAndStaleActor(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.SetDisabled(ctx, f.admin, f.admin.ID, true, "disable-last"); !errors.Is(err, identity.ErrLastAdministrator) {
		t.Fatalf("last admin disabled: %v", err)
	}
	if _, err := f.service.ChangeRole(ctx, f.admin, f.admin.ID, identity.RoleStandard, nil, "demote-last"); !errors.Is(err, identity.ErrLastAdministrator) {
		t.Fatalf("last admin demoted: %v", err)
	}
	other := f.user(t, "other_admin", identity.RoleAdmin, nil)
	if _, err := f.service.ChangeRole(ctx, other, f.admin.ID, identity.RoleStandard, nil, "demote"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CreateUser(ctx, f.admin, identity.CreateUserInput{Username: "stale_actor", Password: testPassword}, "stale-request"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("stale admin actor accepted: %v", err)
	}
	if _, err := f.service.Users(ctx, f.admin, 20, 0); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("stale admin read accounts: %v", err)
	}
	if _, err := f.service.Audit(ctx, f.admin, 20, 0); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("stale admin read audit: %v", err)
	}
}

func TestIdentityConcurrentLastAdministrator(t *testing.T) {
	f := newFixture(t)
	other := f.user(t, "other_admin", identity.RoleAdmin, nil)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, actor := range []identity.User{f.admin, other} {
		go func() {
			<-start
			_, err := f.service.ChangeRole(context.Background(), actor, actor.ID, identity.RoleStandard, nil, "concurrent-demotion")
			results <- err
		}()
	}
	close(start)
	var successes, prevented int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, identity.ErrLastAdministrator):
			prevented++
		default:
			t.Fatalf("concurrent demotion: %v", err)
		}
	}
	var active int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM users WHERE role='admin' AND NOT disabled").Scan(&active); err != nil {
		t.Fatal(err)
	}
	if successes != 1 || prevented != 1 || active != 1 {
		t.Fatalf("successes=%d prevented=%d active=%d, want 1 each", successes, prevented, active)
	}
}

func TestIdentityAuditFailureRollsBack(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	vip := f.user(t, "vip_before_audit_failure", identity.RoleVIP, nil)
	session := f.login(t, vip.Username)
	if _, err := f.pool.Exec(ctx, "DROP TABLE audit_events"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CreateUser(ctx, f.admin, identity.CreateUserInput{Username: "must_rollback", Password: testPassword}, "audit-failure"); err == nil {
		t.Fatal("mutation without audit accepted")
	}
	var count int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE username='must_rollback'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("account persisted without audit")
	}
	if _, err := f.service.ChangeRole(ctx, f.admin, vip.ID, identity.RoleStandard, nil, "failed-revoke"); err == nil {
		t.Fatal("role mutation without audit accepted")
	}
	p, err := f.service.Authenticate(ctx, session.Token)
	if err != nil || p.User.Role != identity.RoleVIP {
		t.Fatalf("failed transaction changed role: role=%s err=%v", p.User.Role, err)
	}
}

func TestIdentityUsernameAndCredentialFailures(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	u := f.user(t, "  USER_A  ", identity.RoleStandard, nil)
	if u.Username != "user_a" {
		t.Fatal("username not normalized")
	}
	if _, err := f.service.CreateUser(ctx, f.admin, identity.CreateUserInput{Username: "USER_A", Password: testPassword}, "duplicate"); !errors.Is(err, identity.ErrConflict) {
		t.Fatalf("duplicate result: %v", err)
	}
	for _, name := range []string{"user_a", "unknown_user"} {
		if _, err := f.service.Login(ctx, name, "wrong-password"); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatalf("credential result: %v", err)
		}
	}
	if _, err := f.service.CreateUser(ctx, f.admin, identity.CreateUserInput{Username: "too_long", Password: strings.Repeat("x", 73)}, "invalid-password"); !errors.Is(err, identity.ErrInvalidInput) {
		t.Fatal("bcrypt truncation boundary accepted")
	}
}

type browser struct {
	client *http.Client
	base   string
	csrf   string
}

func newBrowser(t *testing.T, server *httptest.Server) *browser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Browser clients may share a transport, but must never share cookie jars.
	clientCopy := *server.Client()
	client := &clientCopy
	client.Jar = jar
	client.Timeout = 10 * time.Second
	return &browser{client: client, base: server.URL}
}

func (b *browser) request(t *testing.T, method, path string, body any, sendCSRF bool) (int, []byte, *http.Response) {
	t.Helper()
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, b.base+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if sendCSRF {
		req.Header.Set("X-CSRF-Token", b.csrf)
	}
	response, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if response.Header.Get("X-Request-ID") == "" {
		t.Fatal("missing request identifier")
	}
	return response.StatusCode, content, response
}

func (b *browser) login(t *testing.T, name string) {
	t.Helper()
	status, body, _ := b.request(t, "POST", "/api/v1/auth/login", map[string]string{"username": name, "password": testPassword}, false)
	if status != 200 {
		t.Fatalf("login status=%d", status)
	}
	var data struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	b.csrf = data.CSRF
	if b.csrf == "" {
		t.Fatal("missing CSRF token")
	}
}

func TestIdentityHTTPFlow(t *testing.T) {
	f := newFixture(t)
	a := f.user(t, "user_a", identity.RoleStandard, nil)
	vip := f.user(t, "vip_user", identity.RoleVIP, nil)
	handler, err := httpapi.NewHandler(f.service, httpapi.Config{CSRFKey: bytes.Repeat([]byte{1}, 32), Origin: "http://app.test"}, f.pool.Ping)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	admin, ordinary, member := newBrowser(t, server), newBrowser(t, server), newBrowser(t, server)
	if status, _, _ := ordinary.request(t, "GET", "/api/v1/auth/me", nil, false); status != 401 {
		t.Fatal("anonymous me accepted")
	}
	admin.login(t, "admin")
	ordinary.login(t, a.Username)
	member.login(t, vip.Username)
	if status, _, _ := ordinary.request(t, "GET", "/api/v1/admin/users", nil, false); status != 403 {
		t.Fatal("ordinary admin list accepted")
	}
	if status, _, _ := ordinary.request(t, "POST", "/api/v1/admin/users", map[string]string{"username": "escalated", "password": testPassword, "role": "admin"}, true); status != 403 {
		t.Fatal("ordinary created administrator")
	}
	if status, _, _ := admin.request(t, "POST", "/api/v1/admin/users", map[string]string{"username": "without_csrf", "password": testPassword}, false); status != 403 {
		t.Fatal("CSRF missing accepted")
	}
	status, body, _ := member.request(t, "GET", "/api/v1/auth/me", nil, false)
	var me struct {
		Capabilities  map[string]bool `json:"capabilities"`
		EffectiveRole string          `json:"effective_role"`
	}
	if status != 200 || json.Unmarshal(body, &me) != nil || !me.Capabilities["recommendations.create"] {
		t.Fatal("valid VIP capability missing")
	}
	status, body, _ = member.request(t, "GET", "/api/v1/auth/sessions", nil, false)
	var sessions struct {
		Items []identity.SessionInfo `json:"items"`
	}
	if status != 200 || json.Unmarshal(body, &sessions) != nil || len(sessions.Items) != 1 {
		t.Fatal("session listing failed")
	}
	targetSession := sessions.Items[0].ID
	if status, _, _ := ordinary.request(t, "DELETE", "/api/v1/auth/sessions/"+targetSession, nil, true); status != 404 {
		t.Fatalf("cross-user session status=%d", status)
	}
	if status, _, _ := member.request(t, "GET", "/api/v1/auth/me", nil, false); status != 200 {
		t.Fatal("cross-user request revoked member")
	}
	if status, _, _ := admin.request(t, "PATCH", "/api/v1/admin/users/"+vip.ID+"/role", map[string]string{"role": "standard"}, true); status != 200 {
		t.Fatal("VIP revoke failed")
	}
	status, body, _ = member.request(t, "GET", "/api/v1/auth/me", nil, false)
	if status != 200 || json.Unmarshal(body, &me) != nil || me.Capabilities["recommendations.create"] || me.EffectiveRole != "standard" {
		t.Fatal("old cookie retained VIP capability")
	}
	if status, body, _ := admin.request(t, "GET", "/api/v1/admin/audit-events", nil, false); status != 200 || bytes.Contains(body, []byte(testPassword)) || bytes.Contains(body, []byte("password_hash")) {
		t.Fatal("audit unavailable or leaks password")
	}
	if status, _, _ := admin.request(t, "PATCH", "/api/v1/admin/users/"+a.ID+"/disabled", map[string]bool{"disabled": true}, true); status != 200 {
		t.Fatal("disable failed")
	}
	if status, _, _ := ordinary.request(t, "GET", "/api/v1/auth/me", nil, false); status != 401 {
		t.Fatal("disabled cookie accepted")
	}
	if status, _, _ := member.request(t, "POST", "/api/v1/auth/logout", nil, true); status != 204 {
		t.Fatal("logout failed")
	}
	if status, _, _ := member.request(t, "GET", "/api/v1/auth/me", nil, false); status != 401 {
		t.Fatal("logout cookie accepted")
	}
}

func TestIdentityHTTPInputAndSecureCookie(t *testing.T) {
	f := newFixture(t)
	handler, err := httpapi.NewHandler(f.service, httpapi.Config{CSRFKey: bytes.Repeat([]byte{2}, 32), Origin: "https://app.test", SecureCookies: true}, f.pool.Ping)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	b := newBrowser(t, server)
	status, body, response := b.request(t, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": testPassword}, false)
	if status != 200 {
		t.Fatal("TLS login failed")
	}
	var cookie *http.Cookie
	for _, item := range response.Cookies() {
		if item.Name == httpapi.CookieName {
			cookie = item
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatal("cookie security attributes incorrect")
	}
	if bytes.Contains(body, []byte(cookie.Value)) || bytes.Contains(body, []byte(testPassword)) {
		t.Fatal("login JSON leaked credentials or cookie")
	}
	req, err := http.NewRequest("POST", server.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"bad"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.test")
	r, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := r.Body.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if r.StatusCode != 403 {
		t.Fatal("cross-origin login accepted")
	}
	if status, _, _ := b.request(t, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": testPassword, "role": "admin"}, false); status != 400 {
		t.Fatal("unknown login field accepted")
	}
	if status, _, _ := b.request(t, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": strings.Repeat("x", 17000)}, false); status != 413 {
		t.Fatalf("oversize status=%d", status)
	}
	for range 9 {
		b.request(t, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "wrong"}, false)
	}
	if status, _, _ := b.request(t, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "wrong"}, false); status != 429 {
		t.Fatalf("login rate-limit status=%d", status)
	}
}
