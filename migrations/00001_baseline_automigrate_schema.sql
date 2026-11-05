-- +goose Up
-- +goose StatementBegin
-- Baseline: reproduces the Phase 1 GORM AutoMigrate schema (ADR-012).
-- Idempotent so existing AutoMigrate databases can apply this safely.
IF OBJECT_ID(N'dbo.meta_sources', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.meta_sources (
        id            bigint IDENTITY(1,1) NOT NULL PRIMARY KEY,
        name          nvarchar(200) NOT NULL,
        kind          nvarchar(50)  NOT NULL,
        description   nvarchar(500) NULL,
        params_json   nvarchar(max) NOT NULL,
        is_enabled    bit NOT NULL CONSTRAINT DF_meta_sources_is_enabled DEFAULT (1),
        created_at    datetimeoffset NULL,
        updated_at    datetimeoffset NULL,
        deleted_at    datetimeoffset NULL
    );
    CREATE UNIQUE INDEX idx_meta_sources_name ON dbo.meta_sources(name);
    CREATE INDEX idx_meta_sources_kind ON dbo.meta_sources(kind);
    CREATE INDEX idx_meta_sources_deleted_at ON dbo.meta_sources(deleted_at);
END;

IF OBJECT_ID(N'dbo.meta_loads', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.meta_loads (
        id                bigint IDENTITY(1,1) NOT NULL PRIMARY KEY,
        source_id         bigint NOT NULL,
        name              nvarchar(200) NOT NULL,
        object_name       nvarchar(400) NOT NULL,
        target_schema     nvarchar(100) NOT NULL CONSTRAINT DF_meta_loads_target_schema DEFAULT (N'dbo'),
        target_table      nvarchar(200) NOT NULL,
        mode              nvarchar(20)  NOT NULL,
        watermark_column  nvarchar(200) NULL,
        watermark_type    nvarchar(20)  NOT NULL CONSTRAINT DF_meta_loads_watermark_type DEFAULT (N'none'),
        batch_size        int NOT NULL CONSTRAINT DF_meta_loads_batch_size DEFAULT (1000),
        auto_create_table bit NOT NULL CONSTRAINT DF_meta_loads_auto_create DEFAULT (1),
        cron_expression   nvarchar(100) NOT NULL,
        is_enabled        bit NOT NULL CONSTRAINT DF_meta_loads_is_enabled DEFAULT (1),
        created_at        datetimeoffset NULL,
        updated_at        datetimeoffset NULL,
        deleted_at        datetimeoffset NULL
    );
    CREATE UNIQUE INDEX idx_meta_loads_name ON dbo.meta_loads(name);
    CREATE INDEX idx_meta_loads_source_id ON dbo.meta_loads(source_id);
    CREATE INDEX idx_meta_loads_deleted_at ON dbo.meta_loads(deleted_at);
END;

IF OBJECT_ID(N'dbo.meta_watermarks', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.meta_watermarks (
        load_id         bigint NOT NULL PRIMARY KEY,
        value           nvarchar(100) NOT NULL,
        updated_at      datetimeoffset NULL,
        previous_value  nvarchar(100) NULL,
        reset_at        datetimeoffset NULL,
        reset_reason    nvarchar(500) NULL
    );
END;

IF OBJECT_ID(N'dbo.meta_load_runs', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.meta_load_runs (
        id               bigint IDENTITY(1,1) NOT NULL PRIMARY KEY,
        run_uuid         nvarchar(36) NOT NULL,
        load_id          bigint NOT NULL,
        status           nvarchar(20) NOT NULL,
        started_at       datetimeoffset NOT NULL,
        finished_at      datetimeoffset NULL,
        rows_read        bigint NULL,
        rows_written     bigint NULL,
        watermark_start  nvarchar(100) NULL,
        watermark_end    nvarchar(100) NULL,
        error_message    nvarchar(max) NULL,
        duration_ms      bigint NULL
    );
    CREATE UNIQUE INDEX idx_meta_load_runs_run_uuid ON dbo.meta_load_runs(run_uuid);
    CREATE INDEX idx_meta_load_runs_load_id ON dbo.meta_load_runs(load_id);
    CREATE INDEX idx_meta_load_runs_started_at ON dbo.meta_load_runs(started_at);
END;

IF OBJECT_ID(N'dbo.meta_log_entries', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.meta_log_entries (
        id          bigint IDENTITY(1,1) NOT NULL PRIMARY KEY,
        created_at  datetimeoffset NOT NULL,
        level       nvarchar(10) NOT NULL,
        run_uuid    nvarchar(36) NULL,
        load_id     bigint NULL,
        source_id   bigint NULL,
        category    nvarchar(50) NULL,
        message     nvarchar(max) NOT NULL,
        details     nvarchar(max) NULL
    );
    CREATE INDEX idx_meta_log_entries_created_at ON dbo.meta_log_entries(created_at);
    CREATE INDEX idx_meta_log_entries_level ON dbo.meta_log_entries(level);
    CREATE INDEX idx_meta_log_entries_run_uuid ON dbo.meta_log_entries(run_uuid);
    CREATE INDEX idx_meta_log_entries_load_id ON dbo.meta_log_entries(load_id);
    CREATE INDEX idx_meta_log_entries_source_id ON dbo.meta_log_entries(source_id);
    CREATE INDEX idx_meta_log_entries_category ON dbo.meta_log_entries(category);
END;

IF OBJECT_ID(N'dbo.meta_modbus_tags', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.meta_modbus_tags (
        id              bigint IDENTITY(1,1) NOT NULL PRIMARY KEY,
        load_id         bigint NOT NULL,
        correlation_id  nvarchar(max) NULL,
        tag_name        nvarchar(200) NOT NULL,
        unit_id         tinyint NOT NULL CONSTRAINT DF_meta_modbus_tags_unit_id DEFAULT (1),
        register_type   nvarchar(20) NOT NULL,
        address         int NOT NULL,
        quantity        int NOT NULL CONSTRAINT DF_meta_modbus_tags_quantity DEFAULT (1),
        data_type       nvarchar(20) NOT NULL,
        byte_order      nvarchar(10) NOT NULL CONSTRAINT DF_meta_modbus_tags_byte_order DEFAULT (N'big'),
        word_order      nvarchar(10) NOT NULL CONSTRAINT DF_meta_modbus_tags_word_order DEFAULT (N'high_first'),
        scale           float NOT NULL CONSTRAINT DF_meta_modbus_tags_scale DEFAULT (1),
        offset          float NOT NULL CONSTRAINT DF_meta_modbus_tags_offset DEFAULT (0),
        is_enabled      bit NOT NULL CONSTRAINT DF_meta_modbus_tags_is_enabled DEFAULT (1)
    );
    CREATE INDEX idx_meta_modbus_tags_load_id ON dbo.meta_modbus_tags(load_id);
END;

IF OBJECT_ID(N'dbo.meta_opcua_nodes', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.meta_opcua_nodes (
        id          bigint IDENTITY(1,1) NOT NULL PRIMARY KEY,
        load_id     bigint NOT NULL,
        tag_name    nvarchar(200) NOT NULL,
        node_id     nvarchar(400) NOT NULL,
        scale       float NOT NULL CONSTRAINT DF_meta_opcua_nodes_scale DEFAULT (1),
        offset      float NOT NULL CONSTRAINT DF_meta_opcua_nodes_offset DEFAULT (0),
        is_enabled  bit NOT NULL CONSTRAINT DF_meta_opcua_nodes_is_enabled DEFAULT (1)
    );
    CREATE INDEX idx_meta_opcua_nodes_load_id ON dbo.meta_opcua_nodes(load_id);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
IF OBJECT_ID(N'dbo.meta_opcua_nodes', N'U') IS NOT NULL DROP TABLE dbo.meta_opcua_nodes;
IF OBJECT_ID(N'dbo.meta_modbus_tags', N'U') IS NOT NULL DROP TABLE dbo.meta_modbus_tags;
IF OBJECT_ID(N'dbo.meta_log_entries', N'U') IS NOT NULL DROP TABLE dbo.meta_log_entries;
IF OBJECT_ID(N'dbo.meta_load_runs', N'U') IS NOT NULL DROP TABLE dbo.meta_load_runs;
IF OBJECT_ID(N'dbo.meta_watermarks', N'U') IS NOT NULL DROP TABLE dbo.meta_watermarks;
IF OBJECT_ID(N'dbo.meta_loads', N'U') IS NOT NULL DROP TABLE dbo.meta_loads;
IF OBJECT_ID(N'dbo.meta_sources', N'U') IS NOT NULL DROP TABLE dbo.meta_sources;
-- +goose StatementEnd
