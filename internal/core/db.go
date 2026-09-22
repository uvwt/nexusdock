package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func OpenSQLite(ctx context.Context, path string, maxOpenConns int) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, NewError(CodeValidation, "database path is required", nil)
	}
	dsn := path
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve database path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
		u := &url.URL{Scheme: "file", Path: absolute}
		query := u.Query()
		query.Add("_pragma", "foreign_keys(1)")
		query.Add("_pragma", "busy_timeout(5000)")
		// 控制库以短事务和可靠性优先，不依赖 WAL 提供的高并发写入。
		// 使用 rollback journal + FULL 同步，降低容器挂载等共享文件系统对 WAL/SHM 一致性的要求。
		query.Add("_pragma", "journal_mode(DELETE)")
		query.Add("_pragma", "synchronous(FULL)")
		u.RawQuery = query.Encode()
		dsn = u.String()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if maxOpenConns < 1 {
		maxOpenConns = 1
	}
	if path == ":memory:" {
		maxOpenConns = 1
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxOpenConns)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	return db, nil
}

func IsSQLiteConflict(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "constraint failed") ||
		strings.Contains(text, "database is locked") ||
		errors.Is(err, sql.ErrTxDone)
}

type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type TxFunc func(context.Context, *sql.Tx) error

type TxManager interface {
	WithinTx(context.Context, *sql.TxOptions, TxFunc) error
}

type SQLTxManager struct{ db *sql.DB }

func NewTxManager(db *sql.DB) *SQLTxManager { return &SQLTxManager{db: db} }

func (m *SQLTxManager) WithinTx(ctx context.Context, opts *sql.TxOptions, fn TxFunc) error {
	if fn == nil {
		return NewError(CodeValidation, "transaction callback is required", nil)
	}
	tx, err := m.db.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		if IsSQLiteConflict(err) {
			return NewError(CodeDBConflict, "transaction commit conflict", err)
		}
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}
