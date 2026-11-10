-- +goose Up
-- +goose StatementBegin
-- ADR-022: opt-in historic backfill (dual-cursor sibling loads).
IF COL_LENGTH(N'dbo.meta_loads', N'backfill_required') IS NULL
    ALTER TABLE dbo.meta_loads ADD backfill_required BIT NOT NULL CONSTRAINT df_meta_loads_backfill_required DEFAULT 0;

IF COL_LENGTH(N'dbo.meta_loads', N'backfill_stop') IS NULL
    ALTER TABLE dbo.meta_loads ADD backfill_stop NVARCHAR(100) NULL;

IF COL_LENGTH(N'dbo.meta_loads', N'backfill_completed_at') IS NULL
    ALTER TABLE dbo.meta_loads ADD backfill_completed_at DATETIME2 NULL;

IF COL_LENGTH(N'dbo.meta_loads', N'backfill_of_load_id') IS NULL
    ALTER TABLE dbo.meta_loads ADD backfill_of_load_id BIGINT NULL;

IF COL_LENGTH(N'dbo.meta_loads', N'backfill_ceiling') IS NULL
    ALTER TABLE dbo.meta_loads ADD backfill_ceiling NVARCHAR(100) NULL;

IF NOT EXISTS (
    SELECT 1 FROM sys.foreign_keys WHERE name = N'fk_meta_loads_backfill_of_load_id'
)
BEGIN
    ALTER TABLE dbo.meta_loads WITH CHECK
        ADD CONSTRAINT fk_meta_loads_backfill_of_load_id
        FOREIGN KEY (backfill_of_load_id) REFERENCES dbo.meta_loads(id);
END;

IF NOT EXISTS (
    SELECT 1 FROM sys.indexes
    WHERE name = N'ux_meta_loads_backfill_of_load_id'
      AND object_id = OBJECT_ID(N'dbo.meta_loads')
)
BEGIN
    CREATE UNIQUE INDEX ux_meta_loads_backfill_of_load_id
        ON dbo.meta_loads(backfill_of_load_id)
        WHERE backfill_of_load_id IS NOT NULL;
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
IF EXISTS (
    SELECT 1 FROM sys.indexes
    WHERE name = N'ux_meta_loads_backfill_of_load_id'
      AND object_id = OBJECT_ID(N'dbo.meta_loads')
)
    DROP INDEX ux_meta_loads_backfill_of_load_id ON dbo.meta_loads;

IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = N'fk_meta_loads_backfill_of_load_id')
    ALTER TABLE dbo.meta_loads DROP CONSTRAINT fk_meta_loads_backfill_of_load_id;

IF COL_LENGTH(N'dbo.meta_loads', N'backfill_ceiling') IS NOT NULL
    ALTER TABLE dbo.meta_loads DROP COLUMN backfill_ceiling;
IF COL_LENGTH(N'dbo.meta_loads', N'backfill_of_load_id') IS NOT NULL
    ALTER TABLE dbo.meta_loads DROP COLUMN backfill_of_load_id;
IF COL_LENGTH(N'dbo.meta_loads', N'backfill_completed_at') IS NOT NULL
    ALTER TABLE dbo.meta_loads DROP COLUMN backfill_completed_at;
IF COL_LENGTH(N'dbo.meta_loads', N'backfill_stop') IS NOT NULL
    ALTER TABLE dbo.meta_loads DROP COLUMN backfill_stop;
IF COL_LENGTH(N'dbo.meta_loads', N'backfill_required') IS NOT NULL
BEGIN
    ALTER TABLE dbo.meta_loads DROP CONSTRAINT df_meta_loads_backfill_required;
    ALTER TABLE dbo.meta_loads DROP COLUMN backfill_required;
END;
-- +goose StatementEnd
