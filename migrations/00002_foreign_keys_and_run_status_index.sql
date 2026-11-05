-- +goose Up
-- +goose StatementBegin
-- Explicit foreign keys and an index for meta_load_runs(status, started_at).
IF NOT EXISTS (
    SELECT 1 FROM sys.foreign_keys WHERE name = N'fk_meta_loads_source_id'
)
BEGIN
    ALTER TABLE dbo.meta_loads WITH CHECK
        ADD CONSTRAINT fk_meta_loads_source_id
        FOREIGN KEY (source_id) REFERENCES dbo.meta_sources(id);
END;

IF NOT EXISTS (
    SELECT 1 FROM sys.foreign_keys WHERE name = N'fk_meta_watermarks_load_id'
)
BEGIN
    ALTER TABLE dbo.meta_watermarks WITH CHECK
        ADD CONSTRAINT fk_meta_watermarks_load_id
        FOREIGN KEY (load_id) REFERENCES dbo.meta_loads(id);
END;

IF NOT EXISTS (
    SELECT 1 FROM sys.foreign_keys WHERE name = N'fk_meta_load_runs_load_id'
)
BEGIN
    ALTER TABLE dbo.meta_load_runs WITH CHECK
        ADD CONSTRAINT fk_meta_load_runs_load_id
        FOREIGN KEY (load_id) REFERENCES dbo.meta_loads(id);
END;

IF NOT EXISTS (
    SELECT 1 FROM sys.foreign_keys WHERE name = N'fk_meta_modbus_tags_load_id'
)
BEGIN
    ALTER TABLE dbo.meta_modbus_tags WITH CHECK
        ADD CONSTRAINT fk_meta_modbus_tags_load_id
        FOREIGN KEY (load_id) REFERENCES dbo.meta_loads(id);
END;

IF NOT EXISTS (
    SELECT 1 FROM sys.foreign_keys WHERE name = N'fk_meta_opcua_nodes_load_id'
)
BEGIN
    ALTER TABLE dbo.meta_opcua_nodes WITH CHECK
        ADD CONSTRAINT fk_meta_opcua_nodes_load_id
        FOREIGN KEY (load_id) REFERENCES dbo.meta_loads(id);
END;

IF NOT EXISTS (
    SELECT 1 FROM sys.indexes
    WHERE name = N'idx_meta_load_runs_status_started_at'
      AND object_id = OBJECT_ID(N'dbo.meta_load_runs')
)
BEGIN
    CREATE INDEX idx_meta_load_runs_status_started_at
        ON dbo.meta_load_runs(status, started_at);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
IF EXISTS (
    SELECT 1 FROM sys.indexes
    WHERE name = N'idx_meta_load_runs_status_started_at'
      AND object_id = OBJECT_ID(N'dbo.meta_load_runs')
)
    DROP INDEX idx_meta_load_runs_status_started_at ON dbo.meta_load_runs;

IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = N'fk_meta_opcua_nodes_load_id')
    ALTER TABLE dbo.meta_opcua_nodes DROP CONSTRAINT fk_meta_opcua_nodes_load_id;
IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = N'fk_meta_modbus_tags_load_id')
    ALTER TABLE dbo.meta_modbus_tags DROP CONSTRAINT fk_meta_modbus_tags_load_id;
IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = N'fk_meta_load_runs_load_id')
    ALTER TABLE dbo.meta_load_runs DROP CONSTRAINT fk_meta_load_runs_load_id;
IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = N'fk_meta_watermarks_load_id')
    ALTER TABLE dbo.meta_watermarks DROP CONSTRAINT fk_meta_watermarks_load_id;
IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = N'fk_meta_loads_source_id')
    ALTER TABLE dbo.meta_loads DROP CONSTRAINT fk_meta_loads_source_id;
-- +goose StatementEnd
