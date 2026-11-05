package sink

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/slaghuis/data-loader/internal/sqlident"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

const (
	stagingSuffix = "_stg"
	oldSuffix     = "_old"
)

// Ensure MSSQLSink implements contracts.AtomicReplacer (ADR-020).
var _ contracts.AtomicReplacer = (*MSSQLSink)(nil)

// BeginReplace prepares an empty staging table for an atomic replace.
func (s *MSSQLSink) BeginReplace(ctx context.Context, target contracts.TargetTable, columns []contracts.Column) (contracts.TargetTable, error) {
	if err := validateTarget(target); err != nil {
		return contracts.TargetTable{}, err
	}
	staging, err := stagingTarget(target)
	if err != nil {
		return contracts.TargetTable{}, err
	}

	if err := s.dropTableIfExists(ctx, staging); err != nil {
		return contracts.TargetTable{}, fmt.Errorf("drop staging: %w", err)
	}

	liveExists, err := s.tableExists(ctx, target)
	if err != nil {
		return contracts.TargetTable{}, err
	}
	if liveExists {
		// Clone column definitions only (no indexes/FKs) — ADR-020.
		q := fmt.Sprintf("SELECT * INTO %s.%s FROM %s.%s WHERE 1 = 0",
			sqlident.QuoteMSSQL(staging.Schema), sqlident.QuoteMSSQL(staging.Name),
			sqlident.QuoteMSSQL(target.Schema), sqlident.QuoteMSSQL(target.Name))
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return contracts.TargetTable{}, fmt.Errorf("clone staging schema: %w", err)
		}
		return staging, nil
	}

	if len(columns) == 0 {
		return contracts.TargetTable{}, fmt.Errorf("begin replace: columns required when target %s.%s does not exist", target.Schema, target.Name)
	}
	if err := s.EnsureTable(ctx, staging, columns); err != nil {
		return contracts.TargetTable{}, fmt.Errorf("create staging: %w", err)
	}
	return staging, nil
}

// CommitReplace swaps staging into the live target name inside one transaction.
func (s *MSSQLSink) CommitReplace(ctx context.Context, target, staging contracts.TargetTable) error {
	if err := validateTarget(target); err != nil {
		return err
	}
	if err := validateTarget(staging); err != nil {
		return err
	}

	liveExists, err := s.tableExists(ctx, target)
	if err != nil {
		return err
	}
	stgExists, err := s.tableExists(ctx, staging)
	if err != nil {
		return err
	}
	if !stgExists {
		return fmt.Errorf("commit replace: staging %s.%s does not exist", staging.Schema, staging.Name)
	}

	if !liveExists {
		return s.renameTable(ctx, staging, target.Name)
	}

	old, err := oldTarget(target)
	if err != nil {
		return err
	}
	if err := s.dropTableIfExists(ctx, old); err != nil {
		return fmt.Errorf("drop previous old table: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := renameInTx(ctx, tx, target, old.Name); err != nil {
		return err
	}
	if err := renameInTx(ctx, tx, staging, target.Name); err != nil {
		return err
	}
	drop := fmt.Sprintf("DROP TABLE %s.%s",
		sqlident.QuoteMSSQL(old.Schema), sqlident.QuoteMSSQL(old.Name))
	if _, err := tx.ExecContext(ctx, drop); err != nil {
		return fmt.Errorf("drop old table: %w", err)
	}
	return tx.Commit()
}

// AbortReplace drops the staging table; the live target is untouched.
func (s *MSSQLSink) AbortReplace(ctx context.Context, staging contracts.TargetTable) error {
	if err := validateTarget(staging); err != nil {
		return err
	}
	return s.dropTableIfExists(ctx, staging)
}

func stagingTarget(t contracts.TargetTable) (contracts.TargetTable, error) {
	name := t.Name + stagingSuffix
	if err := sqlident.Validate(name); err != nil {
		return contracts.TargetTable{}, fmt.Errorf("staging table name: %w", err)
	}
	return contracts.TargetTable{Schema: t.Schema, Name: name}, nil
}

func oldTarget(t contracts.TargetTable) (contracts.TargetTable, error) {
	name := t.Name + oldSuffix
	if err := sqlident.Validate(name); err != nil {
		return contracts.TargetTable{}, fmt.Errorf("old table name: %w", err)
	}
	return contracts.TargetTable{Schema: t.Schema, Name: name}, nil
}

func (s *MSSQLSink) dropTableIfExists(ctx context.Context, t contracts.TargetTable) error {
	stmt := fmt.Sprintf("DROP TABLE IF EXISTS %s.%s",
		sqlident.QuoteMSSQL(t.Schema), sqlident.QuoteMSSQL(t.Name))
	_, err := s.db.ExecContext(ctx, stmt)
	return err
}

func (s *MSSQLSink) renameTable(ctx context.Context, from contracts.TargetTable, newName string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := renameInTx(ctx, tx, from, newName); err != nil {
		return err
	}
	return tx.Commit()
}

func renameInTx(ctx context.Context, tx *sql.Tx, from contracts.TargetTable, newName string) error {
	if err := sqlident.Validate(newName); err != nil {
		return fmt.Errorf("rename target: %w", err)
	}
	// sp_rename expects schema.table as the object name and an unqualified new name.
	obj := from.Schema + "." + from.Name
	_, err := tx.ExecContext(ctx, `EXEC sp_rename @objname, @newname;`,
		sql.Named("objname", obj),
		sql.Named("newname", newName),
	)
	if err != nil {
		return fmt.Errorf("sp_rename %s -> %s: %w", obj, newName, err)
	}
	return nil
}
