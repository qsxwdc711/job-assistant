package catalog

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"jobassistant/backend/internal/identity"
)

func readCompanies(ctx context.Context, tx pgx.Tx) ([]Company, error) {
	result := []Company{}
	rows, err := tx.Query(ctx, "SELECT code,name,industry,nature,tiers,basis FROM companies ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Company
		if err := rows.Scan(&c.Code, &c.Name, &c.Industry, &c.Nature, &c.Tiers, &c.Basis); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func readSources(ctx context.Context, tx pgx.Tx) ([]Source, error) {
	result := []Source{}
	rows, err := tx.Query(ctx, `SELECT s.code,s.company_code,s.name,s.official_url,s.coverage,s.enabled,s.last_status,s.last_error,s.last_checked_at,s.last_succeeded_at,
(SELECT count(*) FROM jobs j WHERE j.source_code=s.code AND j.published) FROM sources s ORDER BY s.code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var source Source
		if err := rows.Scan(&source.Code, &source.CompanyCode, &source.Name, &source.OfficialURL, &source.Coverage, &source.Enabled, &source.LastStatus, &source.LastError, &source.LastCheckedAt, &source.LastSucceededAt, &source.JobCount); err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	return result, rows.Err()
}

// SystemSources reads public-source configuration for the explicitly invoked collection CLI.
func (s *Store) SystemSources(ctx context.Context) ([]Source, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	items, err := readSources(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// Sources returns source diagnostics only to a current administrator.
func (s *Store) Sources(ctx context.Context, actorID string, now time.Time) ([]Source, error) {
	tx, _, err := s.authorized(ctx, actorID, identity.ManageCatalog, now)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	items, err := readSources(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// Companies returns administrator-maintained company classifications.
func (s *Store) Companies(ctx context.Context, actorID string, now time.Time) ([]Company, error) {
	tx, _, err := s.authorized(ctx, actorID, identity.ManageCatalog, now)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	items, err := readCompanies(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// ChangeCompany updates classifications and their attribution together with the audit record.
func (s *Store) ChangeCompany(ctx context.Context, actorID, code string, input Company, requestID string, now time.Time) error {
	if strings.TrimSpace(input.Basis) == "" || utf8.RuneCountInString(input.Basis) > 1000 || len(input.Tiers) > 5 {
		return identity.ErrInvalidInput
	}
	tx, _, err := s.authorized(ctx, actorID, identity.ManageCatalog, now)
	if err != nil {
		return err
	}
	defer rollback(tx)
	choices := []struct{ kind, code string }{{"industry", input.Industry}, {"nature", input.Nature}}
	for _, tier := range input.Tiers {
		choices = append(choices, struct{ kind, code string }{"tier", tier})
	}
	for _, choice := range choices {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM dictionary_entries WHERE kind=$1 AND code=$2)", choice.kind, choice.code).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return identity.ErrInvalidInput
		}
	}
	if input.Tiers == nil {
		input.Tiers = []string{}
	}
	result, err := tx.Exec(ctx, "UPDATE companies SET industry=$2,nature=$3,tiers=$4,basis=$5 WHERE code=$1", code, input.Industry, input.Nature, input.Tiers, strings.TrimSpace(input.Basis))
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return identity.ErrNotFound
	}
	if err := identity.AppendAudit(ctx, tx, actorID, "catalog.company.update", code, requestID, map[string]any{"industry": input.Industry, "nature": input.Nature, "tiers": input.Tiers}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetSourceEnabled pauses future publication as well as future collection attempts.
func (s *Store) SetSourceEnabled(ctx context.Context, actorID, code string, enabled bool, requestID string, now time.Time) error {
	tx, _, err := s.authorized(ctx, actorID, identity.ManageCatalog, now)
	if err != nil {
		return err
	}
	defer rollback(tx)
	result, err := tx.Exec(ctx, "UPDATE sources SET enabled=$2 WHERE code=$1", code, enabled)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return identity.ErrNotFound
	}
	if err := identity.AppendAudit(ctx, tx, actorID, "catalog.source.enabled", code, requestID, map[string]bool{"enabled": enabled}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetPublished changes visibility without rewriting official content or personal progress.
func (s *Store) SetPublished(ctx context.Context, actorID, id string, published bool, note, requestID string, now time.Time) error {
	if strings.TrimSpace(note) == "" || utf8.RuneCountInString(note) > 1000 {
		return identity.ErrInvalidInput
	}
	tx, _, err := s.authorized(ctx, actorID, identity.ManageCatalog, now)
	if err != nil {
		return err
	}
	defer rollback(tx)
	result, err := tx.Exec(ctx, "UPDATE jobs SET published=$2,review_note=$3 WHERE id::text=$1", id, published, strings.TrimSpace(note))
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return identity.ErrNotFound
	}
	if err := identity.AppendAudit(ctx, tx, actorID, "catalog.job.publish", id, requestID, map[string]bool{"published": published}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
