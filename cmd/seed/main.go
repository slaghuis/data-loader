package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"log"
	"os"

	_ "github.com/microsoft/go-mssqldb"
	"gopkg.in/yaml.v3"

	"yourmodule/internal/seed"
)

func main() {
	var file string
	flag.StringVar(&file, "file", "seeds/seed.yaml", "seed yaml file")
	flag.Parse()

	data, err := os.ReadFile(file)
	if err != nil {
		log.Fatal(err)
	}

	var cfg seed.SeedFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Fatal(err)
	}

	connStr := os.Getenv("DB_CONNECTION")
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	sourceIDs := make(map[string]int64)

	for _, s := range cfg.Sources {
		paramsJSON, err := json.Marshal(s.Params)
		if err != nil {
			log.Fatal(err)
		}

		var id int64

		err = db.QueryRowContext(ctx, `
			INSERT INTO meta_sources
			(
				name,
				kind,
				description,
				params_json,
				is_enabled,
				created_at,
				updated_at
			)
			OUTPUT INSERTED.id
			VALUES
			(
				@p1,
				@p2,
				@p3,
				@p4,
				@p5,
				SYSUTCDATETIME(),
				SYSUTCDATETIME()
			)
		`,
			s.Name,
			s.Kind,
			s.Description,
			string(paramsJSON),
			s.Enabled,
		).Scan(&id)

		if err != nil {
			log.Fatal(err)
		}

		sourceIDs[s.Name] = id
		log.Printf("inserted source %s (id=%d)", s.Name, id)
	}

	for _, l := range cfg.Loads {
		sourceID := sourceIDs[l.Source]

		_, err := db.ExecContext(ctx, `
			INSERT INTO meta_loads
			(
				source_id,
				name,
				object_name,
				target_schema,
				target_table,
				mode,
				watermark_column,
				watermark_type,
				batch_size,
				auto_create_table,
				cron_expression,
				is_enabled,
				created_at,
				updated_at
			)
			VALUES
			(
				@p1,
				@p2,
				@p3,
				@p4,
				@p5,
				@p6,
				@p7,
				@p8,
				@p9,
				@p10,
				@p11,
				@p12,
				SYSUTCDATETIME(),
				SYSUTCDATETIME()
			)
		`,
			sourceID,
			l.Name,
			l.ObjectName,
			l.TargetSchema,
			l.TargetTable,
			l.Mode,
			l.WatermarkColumn,
			l.WatermarkType,
			l.BatchSize,
			l.AutoCreateTable,
			l.CronExpression,
			l.Enabled,
		)

		if err != nil {
			log.Fatal(err)
		}

		log.Printf("inserted load %s", l.Name)
	}
}