package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/0x48core/gokit/metrics"
)

const (
	metricName  = "database"
	unnamed     = "unnamed"
	sqlcComment = "-- name: "
)

// DBTX is the query interface that both DB and Tx implement. It matches the
// DBTX interface sqlc generates, so sqlc's New accepts either.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var (
	_ DBTX = (*DB)(nil)
	_ DBTX = (*Tx)(nil)
)

// DB is a *sql.DB whose ExecContext, QueryContext, QueryRowContext and BeginTx
// record each query's latency and log slow ones. The other *sql.DB methods,
// such as PingContext, Stats and Close, are available as they are.
type DB struct {
	*sql.DB
	in *instrument
}

// Open opens a connection pool with conf's pool settings and checks it with a
// ping. metricClient may be nil to record no metrics.
func Open(ctx context.Context, conf *Config, metricClient metrics.Client) (*DB, error) {
	if conf == nil || conf.Driver == "" || conf.DSN == "" {
		return nil, errors.New("database: driver and dsn are required")
	}
	c := conf.withDefaults()

	sqlDB, err := sql.Open(c.Driver, c.DSN)
	if err != nil {
		return nil, fmt.Errorf("database: open: %w", err)
	}
	sqlDB.SetMaxOpenConns(c.MaxOpenConns)
	sqlDB.SetMaxIdleConns(c.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(c.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(c.ConnMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, c.PingTimeout)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("database: ping: %w", err)
	}

	return New(sqlDB, metricClient, c.SlowQueryThreshold), nil
}

// New wraps a pool opened elsewhere. slowQueryThreshold may be 0 to log no
// slow queries.
func New(sqlDB *sql.DB, metricClient metrics.Client, slowQueryThreshold time.Duration) *DB {
	return &DB{DB: sqlDB, in: &instrument{metricClient: metricClient, slow: slowQueryThreshold}}
}

func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	start := time.Now()
	res, err := db.DB.ExecContext(ctx, query, args...)
	db.in.observe(ctx, query, start, err)
	return res, err
}

// QueryContext records the time until the first rows are ready, not the time
// spent iterating them.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	start := time.Now()
	rows, err := db.DB.QueryContext(ctx, query, args...)
	db.in.observe(ctx, query, start, err)
	return rows, err
}

func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	start := time.Now()
	row := db.DB.QueryRowContext(ctx, query, args...)
	db.in.observe(ctx, query, start, row.Err())
	return row
}

func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	tx, err := db.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &Tx{Tx: tx, in: db.in}, nil
}

// WithTx runs fn in a transaction. It commits when fn returns nil, and rolls
// back when fn returns an error or panics, re-panicking after the rollback.
func (db *DB) WithTx(ctx context.Context, opts *sql.TxOptions, fn func(ctx context.Context, tx *Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("database: begin: %w", err)
	}

	defer func() {
		if rec := recover(); rec != nil {
			_ = tx.Rollback()
			panic(rec)
		}
	}()

	if err := fn(ctx, tx); err != nil {
		// ErrTxDone means a cancelled ctx already rolled the transaction back.
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return errors.Join(err, fmt.Errorf("database: rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("database: commit: %w", err)
	}
	return nil
}

// Tx is a *sql.Tx whose queries are recorded like DB's.
type Tx struct {
	*sql.Tx
	in *instrument
}

func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	start := time.Now()
	res, err := tx.Tx.ExecContext(ctx, query, args...)
	tx.in.observe(ctx, query, start, err)
	return res, err
}

func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	start := time.Now()
	rows, err := tx.Tx.QueryContext(ctx, query, args...)
	tx.in.observe(ctx, query, start, err)
	return rows, err
}

func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	start := time.Now()
	row := tx.Tx.QueryRowContext(ctx, query, args...)
	tx.in.observe(ctx, query, start, row.Err())
	return row
}

type instrument struct {
	metricClient metrics.Client
	slow         time.Duration
}

func (in *instrument) observe(ctx context.Context, query string, start time.Time, err error) {
	elapsed := time.Since(start)
	name := queryName(ctx, query)

	if in.metricClient != nil {
		status := "ok"
		if err != nil {
			status = "error"
		}
		in.metricClient.Histogram(metricName, elapsed.Seconds(),
			metrics.Tag{Key: "query", Value: name},
			metrics.Tag{Key: "status", Value: status})
	}

	// The SQL and its arguments are left out, since they may hold personal data.
	if in.slow > 0 && elapsed >= in.slow {
		slog.WarnContext(ctx, "slow query", "query", name, "duration_ms", elapsed.Milliseconds())
	}
}

type ctxKeyQueryName struct{}

// WithQueryName names the queries run with ctx in metrics and slow query logs,
// for queries that have no sqlc name comment.
func WithQueryName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, ctxKeyQueryName{}, name)
}

// queryName returns the name set with WithQueryName, else the name in a
// leading sqlc comment such as "-- name: GetOrder :one", else "unnamed". The
// SQL itself is never used, so metrics do not get a series per query text.
func queryName(ctx context.Context, query string) string {
	if name, ok := ctx.Value(ctxKeyQueryName{}).(string); ok && name != "" {
		return name
	}
	rest, ok := strings.CutPrefix(strings.TrimLeft(query, " \t\r\n"), sqlcComment)
	if !ok {
		return unnamed
	}
	line, _, _ := strings.Cut(rest, "\n")
	if fields := strings.Fields(line); len(fields) > 0 {
		return fields[0]
	}
	return unnamed
}
