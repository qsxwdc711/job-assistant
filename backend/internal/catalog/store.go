package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"jobassistant/backend/internal/identity"
)

// Store owns public catalog reads and their transactional authorization.
type Store struct{ pool *pgxpool.Pool }

// NewStore creates a catalog store for an initialized database.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Warn("catalog transaction rollback failed")
	}
}

func (s *Store) authorized(ctx context.Context, actorID string, action identity.Action, now time.Time) (pgx.Tx, identity.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, identity.User{}, fmt.Errorf("begin catalog: %w", err)
	}
	actor, err := identity.CheckAccess(ctx, tx, actorID, action, "", now)
	if err != nil {
		rollback(tx)
		return nil, identity.User{}, err
	}
	return tx, actor, nil
}

type scanner interface{ Scan(...any) error }

const jobColumns = `j.data,j.id::text,c.name,c.industry,c.nature,c.tiers,c.basis,j.version,j.published,
CASE WHEN s.last_status IN ('failed','restricted') THEN 'stale' ELSE j.verification_status END,
j.verified_at,j.updated_at,j.review_note,
CASE WHEN j.deadline_at IS NOT NULL AND j.deadline_at <= $1 THEN 'closed' ELSE j.public_status END`

func scanJob(row scanner) (Job, error) {
	var job Job
	var data []byte
	err := row.Scan(&data, &job.ID, &job.CompanyName, &job.Industry, &job.Nature, &job.Tiers, &job.CompanyBasis, &job.Version, &job.Published, &job.VerificationStatus, &job.VerifiedAt, &job.UpdatedAt, &job.ReviewNote, &job.PublicStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, identity.ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("read job: %w", err)
	}
	status := job.PublicStatus
	if err := json.Unmarshal(data, &job.Document); err != nil {
		return Job{}, fmt.Errorf("decode job: %w", err)
	}
	job.PublicStatus = status
	return job, nil
}

const jobJoins = ` FROM jobs j JOIN companies c ON c.code=j.company_code JOIN sources s ON s.code=j.source_code `

// LoadJobs reads referenced jobs in a caller's already-authorized transaction, including hidden history.
func LoadJobs(ctx context.Context, tx pgx.Tx, ids []string, now time.Time) (map[string]Job, error) {
	result := make(map[string]Job, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := tx.Query(ctx, "SELECT "+jobColumns+jobJoins+"WHERE j.id::text=ANY($2)", now, ids)
	if err != nil {
		return nil, fmt.Errorf("load referenced jobs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		result[job.ID] = job
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read referenced jobs: %w", err)
	}
	return result, nil
}

// Job returns a published job, or hidden content only for a current administrator.
func (s *Store) Job(ctx context.Context, actorID, id string, now time.Time) (Job, error) {
	tx, actor, err := s.authorized(ctx, actorID, identity.BrowseJobs, now)
	if err != nil {
		return Job{}, err
	}
	defer rollback(tx)
	job, err := scanJob(tx.QueryRow(ctx, "SELECT "+jobColumns+jobJoins+"WHERE j.id::text=$2 AND (j.published OR $3)", now, id, actor.EffectiveRole(now) == identity.RoleAdmin))
	if err != nil {
		return Job{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, fmt.Errorf("commit job read: %w", err)
	}
	return job, nil
}

func whereFilter(f Filter, now time.Time) (string, []any) {
	args := []any{now}
	conditions := []string{"j.published", "$1::timestamptz IS NOT NULL"}
	if f.IncludeHidden {
		conditions = conditions[1:]
	}
	add := func(value any) string { args = append(args, value); return "$" + strconv.Itoa(len(args)) }
	literal := func(value string) string {
		return "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(value) + "%"
	}
	if f.Query != "" {
		p := add(literal(f.Query))
		conditions = append(conditions, "(j.title ILIKE "+p+" OR EXISTS (SELECT 1 FROM job_variants qv WHERE qv.job_id=j.id AND concat_ws(' ',qv.data->>'name',qv.data->>'responsibilities',qv.data->>'requirements',qv.data->>'preferred') ILIKE "+p+"))")
	}
	if f.Company != "" {
		p := add(literal(f.Company))
		conditions = append(conditions, "c.name ILIKE "+p)
	}
	for _, entry := range []struct {
		column string
		values []string
	}{{"c.industry", f.Industries}, {"c.nature", f.Natures}, {"j.recruitment_type", f.RecruitmentTypes}, {"j.written_test", f.WrittenTests}} {
		if len(entry.values) > 0 {
			conditions = append(conditions, entry.column+"=ANY("+add(entry.values)+")")
		}
	}
	if len(f.Tiers) > 0 {
		conditions = append(conditions, "c.tiers && "+add(f.Tiers)+"::text[]")
	}
	states := f.States
	if len(states) == 0 {
		states = []string{"open", "unknown"}
	}
	all := false
	for _, state := range states {
		if state == "all" {
			all = true
		}
	}
	if !all {
		conditions = append(conditions, "(CASE WHEN j.deadline_at IS NOT NULL AND j.deadline_at<=$1 THEN 'closed' ELSE j.public_status END)=ANY("+add(states)+")")
	}
	if f.DeadlineBefore != "" {
		p := add(f.DeadlineBefore)
		conditions = append(conditions, "j.deadline_date IS NOT NULL AND j.deadline_date<="+p+"::date")
	}
	if f.GraduationYear != 0 {
		p := add(strconv.Itoa(f.GraduationYear))
		match := "EXISTS (SELECT 1 FROM jsonb_array_elements(j.data->'graduation_rules') g WHERE left(g->>'from',4)<=" + p + " AND left(g->>'through',4)>=" + p + ")"
		if f.IncludeUnknown {
			match = "(" + match + " OR COALESCE(jsonb_array_length(j.data->'graduation_rules'),0)=0)"
		}
		conditions = append(conditions, match)
	}
	variants := []string{"v.job_id=j.id"}
	if len(f.Directions) > 0 {
		variants = append(variants, "v.direction=ANY("+add(f.Directions)+")")
	}
	if len(f.Majors) > 0 {
		match := "(v.major_kind IN ('unrestricted','preferred') OR v.major_tags && " + add(f.Majors) + "::text[])"
		if f.IncludeUnknown {
			match = "(" + match + " OR v.major_kind='unknown')"
		}
		variants = append(variants, match)
	}
	if len(f.Cities) > 0 {
		p := add(f.Cities)
		variants = append(variants, "EXISTS (SELECT 1 FROM job_locations l WHERE l.job_id=v.job_id AND l.variant_key=v.variant_key AND (l.city=ANY("+p+") OR l.code=ANY("+p+") OR l.kind=ANY("+p+")))")
	}
	if len(variants) > 1 {
		conditions = append(conditions, "EXISTS (SELECT 1 FROM job_variants v WHERE "+strings.Join(variants, " AND ")+")")
	}
	return "WHERE " + strings.Join(conditions, " AND "), args
}

// Jobs applies combined filters in SQL, matching direction and city inside the same variant.
func (s *Store) Jobs(ctx context.Context, actorID string, f Filter, now time.Time) (Page, error) {
	page := Page{Items: []Job{}, Limit: f.Limit, Offset: f.Offset}
	if f.Limit < 1 || f.Limit > 100 || f.Offset < 0 {
		return page, identity.ErrInvalidInput
	}
	action := identity.BrowseJobs
	if f.IncludeHidden {
		action = identity.ManageCatalog
	}
	tx, _, err := s.authorized(ctx, actorID, action, now)
	if err != nil {
		return page, err
	}
	defer rollback(tx)
	where, args := whereFilter(f, now)
	if err := tx.QueryRow(ctx, "SELECT count(*)"+jobJoins+where, args...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("count jobs: %w", err)
	}
	order := "j.updated_at DESC,j.id"
	if f.Sort == "deadline" {
		order = "j.deadline_date ASC NULLS LAST,j.updated_at DESC,j.id"
	}
	args = append(args, f.Limit, f.Offset)
	query := "SELECT " + jobColumns + jobJoins + where + " ORDER BY " + order + " LIMIT $" + strconv.Itoa(len(args)-1) + " OFFSET $" + strconv.Itoa(len(args))
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return page, fmt.Errorf("query jobs: %w", err)
	}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return page, err
		}
		page.Items = append(page.Items, job)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, fmt.Errorf("read jobs: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return page, fmt.Errorf("commit job list: %w", err)
	}
	return page, nil
}

// FilterChoices contains extensible dictionaries and observed places, without invented jobs.
type FilterChoices struct {
	Dictionary []DictionaryEntry `json:"dictionary"`
	Cities     []Location        `json:"cities"`
	Companies  []Company         `json:"companies"`
}

// Filters returns currently available choices to an authenticated user.
func (s *Store) Filters(ctx context.Context, actorID string, now time.Time) (FilterChoices, error) {
	result := FilterChoices{Dictionary: []DictionaryEntry{}, Cities: []Location{}, Companies: []Company{}}
	tx, _, err := s.authorized(ctx, actorID, identity.BrowseJobs, now)
	if err != nil {
		return result, err
	}
	defer rollback(tx)
	rows, err := tx.Query(ctx, "SELECT kind,code,name FROM dictionary_entries ORDER BY kind,name")
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var e DictionaryEntry
		if err := rows.Scan(&e.Kind, &e.Code, &e.Name); err != nil {
			rows.Close()
			return result, err
		}
		result.Dictionary = append(result.Dictionary, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = tx.Query(ctx, `SELECT DISTINCT l.code,l.kind,l.country,l.province,l.city FROM job_locations l JOIN jobs j ON j.id=l.job_id WHERE j.published ORDER BY l.kind,l.city`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var l Location
		if err := rows.Scan(&l.Code, &l.Kind, &l.Country, &l.Province, &l.City); err != nil {
			rows.Close()
			return result, err
		}
		result.Cities = append(result.Cities, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	result.Companies, err = readCompanies(ctx, tx)
	if err != nil {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}
