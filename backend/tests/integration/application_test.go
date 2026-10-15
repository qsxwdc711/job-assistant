package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"jobassistant/backend/internal/application"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
)

func TestApplicationsOwnershipConflictAndClosedJobHistory(t *testing.T) {
	f := newFixture(t)
	catalogStore := publishEvidence(t, f, evidenceJob())
	ctx := context.Background()
	now := time.Now().UTC()
	a := f.user(t, "apply-a", identity.RoleStandard, nil)
	b := f.user(t, "apply-b", identity.RoleVIP, nil)
	page, _ := catalogStore.Jobs(ctx, a.ID, catalog.Filter{Limit: 20}, now)
	jobID := page.Items[0].ID
	store := application.NewStore(f.pool)
	scopeA := application.Scope{ActorID: a.ID}
	scopeB := application.Scope{ActorID: b.ID}
	if err := store.SetFavorite(ctx, scopeA, jobID, true, "test", now); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFavorite(ctx, scopeA, jobID, true, "test", now); err != nil {
		t.Fatal("favorite not idempotent", err)
	}
	favs, err := store.Favorites(ctx, scopeB, 20, 0, now)
	if err != nil || len(favs) != 0 {
		t.Fatalf("favorites leaked: %#v %v", favs, err)
	}
	record, err := store.Create(ctx, scopeA, application.Input{JobID: jobID, Status: "draft"}, "test", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, scopeA, application.Input{JobID: jobID, Status: "draft"}, "test", now); !errors.Is(err, application.ErrAlreadyExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := store.Get(ctx, scopeB, record.ID, now); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("private read: %v", err)
	}
	if _, err := store.Update(ctx, scopeB, record.ID, application.Input{Status: "offer", Version: 1}, "test", now); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("private write: %v", err)
	}
	if _, err := store.Events(ctx, scopeB, record.ID, now); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("private history: %v", err)
	}
	input := application.Input{Status: "submitted", Notes: "我的备注", NextAction: "准备笔试", Version: 1}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Update(ctx, scopeA, record.ID, input, "race", now)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, application.ErrVersionConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent edits: %d %d", success, conflict)
	}
	doc := evidenceJob()
	doc.PublicStatus = "closed"
	publishEvidence(t, f, doc)
	record, err = store.Get(ctx, scopeA, record.ID, now)
	if err != nil || record.Status != "submitted" || record.Job.PublicStatus != "closed" {
		t.Fatalf("closed job changed progress: %#v %v", record, err)
	}
	events, err := store.Events(ctx, scopeA, record.ID, now)
	if err != nil || len(events) != 2 || events[0].Version != 2 {
		t.Fatalf("history: %#v %v", events, err)
	}
	if err := store.Delete(ctx, scopeB, record.ID, 2, "test", now); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("private delete: %v", err)
	}
}

func TestApplicationsRecheckDisabledAndAdministratorScope(t *testing.T) {
	f := newFixture(t)
	catalogStore := publishEvidence(t, f, evidenceJob())
	ctx := context.Background()
	now := time.Now().UTC()
	u := f.user(t, "scope-user", identity.RoleStandard, nil)
	page, _ := catalogStore.Jobs(ctx, u.ID, catalog.Filter{Limit: 20}, now)
	store := application.NewStore(f.pool)
	scope := application.Scope{ActorID: u.ID}
	record, err := store.Create(ctx, scope, application.Input{JobID: page.Items[0].ID, Status: "draft"}, "test", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, application.Scope{ActorID: u.ID, RequireAdmin: true}, record.ID, now); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("admin route accepted: %v", err)
	}
	if _, err := store.Get(ctx, application.Scope{ActorID: f.admin.ID, RequireAdmin: true}, record.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SetDisabled(ctx, f.admin, u.ID, true, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, scope, record.ID, now); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("disabled stale caller: %v", err)
	}
}

func TestAdministratorFavoriteAuditIsAtomic(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	catalogStore := publishEvidence(t, f, evidenceJob())
	u := f.user(t, "favorite-owner", identity.RoleStandard, nil)
	page, err := catalogStore.Jobs(ctx, u.ID, catalog.Filter{Limit: 20}, now)
	if err != nil {
		t.Fatal(err)
	}
	jobID := page.Items[0].ID
	store := application.NewStore(f.pool)
	scope := application.Scope{ActorID: f.admin.ID, OwnerID: u.ID, RequireAdmin: true}
	if err := store.SetFavorite(ctx, scope, jobID, true, "test", now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='favorite.set' AND target_id=$1 AND actor_user_id=$2", jobID, f.admin.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("administrator favorite audit count: %d", count)
	}
	if _, err := f.pool.Exec(ctx, `CREATE FUNCTION reject_favorite_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'test audit unavailable'; END$$;
CREATE TRIGGER reject_favorite_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_favorite_audit()`); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFavorite(ctx, scope, jobID, false, "test", now); err == nil {
		t.Fatal("favorite committed without required audit")
	}
	items, err := store.Favorites(ctx, application.Scope{ActorID: u.ID}, 20, 0, now)
	if err != nil || len(items) != 1 {
		t.Fatalf("audit failure lost favorite: %#v %v", items, err)
	}
}
