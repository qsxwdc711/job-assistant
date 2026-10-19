package task

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

var retryableCollectionCodes = []string{
	"network_error", "timeout", "timeout_or_cancelled", "source_busy", "interrupted",
}

// planRecoveries preserves the original failure and budgets two later tasks per day.
func (s *CollectionStore) planRecoveries(ctx context.Context, now time.Time) error {
	day, start, _ := DayTimes(now)
	if !now.Before(start.Add(14 * time.Hour)) {
		return nil
	}
	midnight := start.Add(-8 * time.Hour)
	_, err := s.pool.Exec(ctx, `INSERT INTO scheduled_collections
(schedule_key,source_code,domain,available_at,created_at,updated_at,recovery_parent_id,recovery_round)
SELECT 'recovery:'||$1||':'||p.source_code||':'||(p.recovery_round+1)::text,
p.source_code,p.domain,$2,$2,$2,p.id,p.recovery_round+1
FROM scheduled_collections p JOIN sources s ON s.code=p.source_code
WHERE s.enabled AND p.state='failed' AND p.attempts=3 AND p.error_code=ANY($5::text[])
AND p.recovery_round<2 AND p.created_at >= $3 AND p.created_at < $4
AND p.schedule_key=CASE WHEN p.recovery_round=0 THEN 'daily:'||$1||':'||p.source_code
 ELSE 'recovery:'||$1||':'||p.source_code||':1' END
AND (p.recovery_round=0 OR EXISTS(SELECT 1 FROM scheduled_collections root WHERE root.id=p.recovery_parent_id
 AND root.source_code=p.source_code AND root.recovery_round=0 AND root.schedule_key='daily:'||$1||':'||p.source_code
 AND root.state='failed' AND root.attempts=3 AND root.error_code=ANY($5::text[])
 AND root.created_at>=$3 AND root.created_at<$4 AND root.updated_at+interval '30 minutes'<=p.created_at))
AND p.updated_at+CASE WHEN p.recovery_round=0 THEN interval '30 minutes' ELSE interval '2 hours' END <= $2
AND (s.last_succeeded_at IS NULL OR s.last_succeeded_at<p.updated_at)
AND NOT (s.last_checked_at>=p.updated_at AND
 (s.last_status IN ('running','restricted') OR (s.last_status='failed' AND NOT(s.last_error=ANY($5::text[])))))
AND NOT EXISTS(SELECT 1 FROM scheduled_collections active WHERE active.source_code=p.source_code AND active.state IN ('queued','running'))
AND NOT EXISTS(SELECT 1 FROM scheduled_collections newer WHERE newer.source_code=p.source_code AND newer.id<>p.id AND newer.updated_at>=p.updated_at
 AND (newer.state='succeeded' OR (newer.state='failed' AND NOT(newer.error_code=ANY($5::text[])))))
ON CONFLICT DO NOTHING`, day, now, midnight, midnight.Add(24*time.Hour), retryableCollectionCodes)
	return err
}

// cancelInvalidRecoveries rechecks both scheduled and CLI results. A live lease
// remains owned by its reader, including when the date changes during a read.
// An empty id checks the queue; a selected id is rechecked under the source lock.
func cancelInvalidRecoveries(ctx context.Context, tx pgx.Tx, now time.Time, id string) (int64, error) {
	day, start, _ := DayTimes(now)
	midnight := start.Add(-8 * time.Hour)
	result, err := tx.Exec(ctx, `WITH decisions AS (
 SELECT t.id,CASE
 WHEN NOT s.enabled THEN 'source_paused'
 WHEN t.created_at<$3 OR t.created_at>=$4 THEN 'recovery_expired'
 WHEN t.schedule_key<>'recovery:'||$1||':'||t.source_code||':'||t.recovery_round::text
 OR p.id IS NULL OR p.source_code<>t.source_code OR p.recovery_round<>t.recovery_round-1
 OR p.created_at<$3 OR p.created_at>=$4 OR p.state<>'failed' OR p.attempts<>3 OR NOT(p.error_code=ANY($5::text[]))
 OR p.schedule_key<>CASE WHEN p.recovery_round=0 THEN 'daily:'||$1||':'||t.source_code
 ELSE 'recovery:'||$1||':'||t.source_code||':1' END
 OR (p.recovery_round=1 AND NOT EXISTS(SELECT 1 FROM scheduled_collections root WHERE root.id=p.recovery_parent_id
 AND root.source_code=p.source_code AND root.recovery_round=0 AND root.schedule_key='daily:'||$1||':'||p.source_code
 AND root.state='failed' AND root.attempts=3 AND root.error_code=ANY($5::text[])
 AND root.created_at>=$3 AND root.created_at<$4 AND root.updated_at+interval '30 minutes'<=p.created_at))
 OR p.updated_at+CASE WHEN p.recovery_round=0 THEN interval '30 minutes' ELSE interval '2 hours' END>$2 THEN 'recovery_parent_invalid'
 WHEN s.last_succeeded_at>=p.updated_at OR EXISTS(
 SELECT 1 FROM scheduled_collections newer WHERE newer.source_code=t.source_code AND newer.id<>t.id AND newer.state='succeeded' AND newer.updated_at>=p.updated_at)
 THEN 'recovery_superseded'
 WHEN (s.last_checked_at>=p.updated_at AND (s.last_status='restricted' OR (s.last_status='failed' AND NOT(s.last_error=ANY($5::text[])))))
 OR EXISTS(SELECT 1 FROM scheduled_collections newer WHERE newer.source_code=t.source_code AND newer.id<>t.id AND newer.id<>p.id
 AND newer.state='failed' AND newer.updated_at>=p.updated_at AND NOT(newer.error_code=ANY($5::text[]))) THEN 'recovery_source_blocked'
 ELSE '' END AS reason
 FROM scheduled_collections t JOIN sources s ON s.code=t.source_code
 LEFT JOIN scheduled_collections p ON p.id=t.recovery_parent_id
 WHERE t.recovery_round>0 AND (t.state='queued' OR(t.state='running' AND t.lease_until<=$2))
 AND ($6='' OR t.id=NULLIF($6,'')::uuid)
)
UPDATE scheduled_collections t SET state='cancelled',error_code=d.reason,updated_at=$2,lease_until=NULL,lease_token=''
FROM decisions d WHERE t.id=d.id AND d.reason<>'' AND (t.state='queued' OR(t.state='running' AND t.lease_until<=$2))`,
		day, now, midnight, midnight.Add(24*time.Hour), retryableCollectionCodes, id)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// recoveryWaiting prevents a deferred retry from overtaking manual or CLI work.
func recoveryWaiting(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	var waiting bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
 SELECT 1 FROM scheduled_collections t JOIN scheduled_collections p ON p.id=t.recovery_parent_id JOIN sources s ON s.code=t.source_code
 WHERE t.id=$1 AND t.recovery_round>0 AND
 ((s.last_status='running' AND s.last_checked_at>=p.updated_at) OR EXISTS(
 SELECT 1 FROM scheduled_collections other WHERE other.source_code=t.source_code AND other.id<>t.id AND other.state IN ('queued','running'))))`, id).Scan(&waiting)
	return waiting, err
}
