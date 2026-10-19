ALTER TABLE scheduled_collections
    ADD COLUMN recovery_parent_id uuid REFERENCES scheduled_collections(id),
    ADD COLUMN recovery_round smallint NOT NULL DEFAULT 0,
    ADD CONSTRAINT collection_recovery_round CHECK(recovery_round BETWEEN 0 AND 2),
    ADD CONSTRAINT collection_recovery_parent CHECK((recovery_round=0) = (recovery_parent_id IS NULL)),
    ADD CONSTRAINT collection_recovery_child_unique UNIQUE(recovery_parent_id),
    ADD CONSTRAINT collection_recovery_not_self CHECK(recovery_parent_id IS NULL OR recovery_parent_id<>id);

CREATE INDEX collection_terminal_source ON scheduled_collections(source_code,updated_at)
    WHERE state IN ('succeeded','failed');
