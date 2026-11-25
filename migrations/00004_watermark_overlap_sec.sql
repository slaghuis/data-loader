-- +goose Up
-- +goose StatementBegin
-- ADR-023: opt-in datetime watermark overlap window (query lower bound only).
IF COL_LENGTH(N'dbo.meta_loads', N'watermark_overlap_sec') IS NULL
    ALTER TABLE dbo.meta_loads ADD watermark_overlap_sec INT NOT NULL
        CONSTRAINT df_meta_loads_watermark_overlap_sec DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
IF COL_LENGTH(N'dbo.meta_loads', N'watermark_overlap_sec') IS NOT NULL
BEGIN
    ALTER TABLE dbo.meta_loads DROP CONSTRAINT df_meta_loads_watermark_overlap_sec;
    ALTER TABLE dbo.meta_loads DROP COLUMN watermark_overlap_sec;
END;
-- +goose StatementEnd
