package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/0x48core/gokit/metrics"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/suite"
)

const getOrder = "-- name: GetOrder :one\nSELECT id FROM orders WHERE id = $1"

type TestDatabaseSuite struct {
	suite.Suite
	mock       sqlmock.Sqlmock
	db         *DB
	reg        *prometheus.Registry
	logs       *bytes.Buffer
	oldDefault *slog.Logger
}

func TestTestDatabaseSuite(t *testing.T) {
	suite.Run(t, new(TestDatabaseSuite))
}

func (suite *TestDatabaseSuite) SetupTest() {
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	suite.Require().NoError(err)
	suite.mock = mock
	suite.reg = prometheus.NewRegistry()
	suite.db = New(sqlDB, metrics.NewClientWithRegisterer("test", "test", suite.reg), 0)

	suite.logs = &bytes.Buffer{}
	suite.oldDefault = slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(suite.logs, nil)))
}

func (suite *TestDatabaseSuite) TearDownTest() {
	slog.SetDefault(suite.oldDefault)
	suite.NoError(suite.mock.ExpectationsWereMet())
	_ = suite.db.Close()
}

// samples returns the sample count of each "query status" label pair.
func (suite *TestDatabaseSuite) samples() map[string]uint64 {
	families, err := suite.reg.Gather()
	suite.Require().NoError(err)
	out := map[string]uint64{}
	for _, f := range families {
		suite.Equal("test_database", f.GetName())
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			out[labels["query"]+" "+labels["status"]] = m.GetHistogram().GetSampleCount()
		}
	}
	return out
}

func (suite *TestDatabaseSuite) Test_QueryName() {
	ctx := context.Background()
	for query, want := range map[string]string{
		getOrder:                          "GetOrder",
		"\n  -- name: ListOrders :many\n": "ListOrders",
		"-- name: Bare":                   "Bare",
		"-- name: \nSELECT 1":             unnamed,
		"SELECT 1":                        unnamed,
		"-- just a comment\nSELECT 1":     unnamed,
	} {
		suite.Equal(want, queryName(ctx, query), query)
	}
	suite.Equal("orders.count", queryName(WithQueryName(ctx, "orders.count"), getOrder))
}

func (suite *TestDatabaseSuite) Test_QueryRowContext() {
	// Arrange
	suite.mock.ExpectQuery(getOrder).WithArgs(42).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))

	// Action
	var id int
	err := suite.db.QueryRowContext(context.Background(), getOrder, 42).Scan(&id)

	// Assert
	suite.Require().NoError(err)
	suite.Equal(42, id)
	suite.Equal(map[string]uint64{"GetOrder ok": 1}, suite.samples())
}

func (suite *TestDatabaseSuite) Test_QueryRowContext_Error() {
	// Arrange
	suite.mock.ExpectQuery(getOrder).WillReturnError(errors.New("connection reset"))

	// Action
	var id int
	err := suite.db.QueryRowContext(context.Background(), getOrder, 42).Scan(&id)

	// Assert
	suite.ErrorContains(err, "connection reset")
	suite.Equal(map[string]uint64{"GetOrder error": 1}, suite.samples())
}

func (suite *TestDatabaseSuite) Test_QueryContext() {
	// Arrange
	query := "SELECT id FROM orders"
	suite.mock.ExpectQuery(query).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2))
	ctx := WithQueryName(context.Background(), "orders.list")

	// Action
	rows, err := suite.db.QueryContext(ctx, query)
	suite.Require().NoError(err)
	var ids []int
	for rows.Next() {
		var id int
		suite.Require().NoError(rows.Scan(&id))
		ids = append(ids, id)
	}
	suite.Require().NoError(rows.Err())
	rows.Close()

	// Assert
	suite.Equal([]int{1, 2}, ids)
	suite.Equal(map[string]uint64{"orders.list ok": 1}, suite.samples())
}

func (suite *TestDatabaseSuite) Test_ExecContext() {
	// Arrange
	query := "-- name: DeleteOrder :exec\nDELETE FROM orders WHERE id = $1"
	suite.mock.ExpectExec(query).WithArgs(7).WillReturnResult(sqlmock.NewResult(0, 1))

	// Action
	res, err := suite.db.ExecContext(context.Background(), query, 7)

	// Assert
	suite.Require().NoError(err)
	n, _ := res.RowsAffected()
	suite.Equal(int64(1), n)
	suite.Equal(map[string]uint64{"DeleteOrder ok": 1}, suite.samples())
}

func (suite *TestDatabaseSuite) Test_WithTx_Commit() {
	// Arrange
	insert := "-- name: CreateOrder :exec\nINSERT INTO orders (id) VALUES ($1)"
	suite.mock.ExpectBegin()
	suite.mock.ExpectExec(insert).WithArgs(1).WillReturnResult(sqlmock.NewResult(1, 1))
	suite.mock.ExpectQuery(getOrder).WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	suite.mock.ExpectCommit()

	// Action
	err := suite.db.WithTx(context.Background(), nil, func(ctx context.Context, tx *Tx) error {
		if _, err := tx.ExecContext(ctx, insert, 1); err != nil {
			return err
		}
		var id int
		return tx.QueryRowContext(ctx, getOrder, 1).Scan(&id)
	})

	// Assert
	suite.Require().NoError(err)
	suite.Equal(map[string]uint64{"CreateOrder ok": 1, "GetOrder ok": 1}, suite.samples())
}

func (suite *TestDatabaseSuite) Test_WithTx_RollbackOnError() {
	// Arrange
	suite.mock.ExpectBegin()
	suite.mock.ExpectRollback()
	errFn := errors.New("out of stock")

	// Action
	err := suite.db.WithTx(context.Background(), nil, func(context.Context, *Tx) error { return errFn })

	// Assert
	suite.Equal(errFn, err)
}

func (suite *TestDatabaseSuite) Test_WithTx_RollbackFails() {
	// Arrange
	suite.mock.ExpectBegin()
	suite.mock.ExpectRollback().WillReturnError(errors.New("bad connection"))
	errFn := errors.New("out of stock")

	// Action
	err := suite.db.WithTx(context.Background(), nil, func(context.Context, *Tx) error { return errFn })

	// Assert
	suite.ErrorIs(err, errFn)
	suite.ErrorContains(err, "database: rollback: bad connection")
}

func (suite *TestDatabaseSuite) Test_WithTx_RollbackOnPanic() {
	// Arrange
	suite.mock.ExpectBegin()
	suite.mock.ExpectRollback()

	// Action & Assert
	suite.PanicsWithValue("boom", func() {
		_ = suite.db.WithTx(context.Background(), nil, func(context.Context, *Tx) error { panic("boom") })
	})
}

func (suite *TestDatabaseSuite) Test_WithTx_BeginFails() {
	// Arrange
	suite.mock.ExpectBegin().WillReturnError(errors.New("too many connections"))
	called := false

	// Action
	err := suite.db.WithTx(context.Background(), nil, func(context.Context, *Tx) error {
		called = true
		return nil
	})

	// Assert
	suite.ErrorContains(err, "database: begin: too many connections")
	suite.False(called)
}

func (suite *TestDatabaseSuite) Test_WithTx_CommitFails() {
	// Arrange
	suite.mock.ExpectBegin()
	suite.mock.ExpectCommit().WillReturnError(errors.New("serialization failure"))

	// Action
	err := suite.db.WithTx(context.Background(), nil, func(context.Context, *Tx) error { return nil })

	// Assert
	suite.ErrorContains(err, "database: commit: serialization failure")
}

func (suite *TestDatabaseSuite) Test_Tx_QueryContext() {
	// Arrange
	query := "-- name: ListOrders :many\nSELECT id FROM orders"
	suite.mock.ExpectBegin()
	suite.mock.ExpectQuery(query).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	suite.mock.ExpectRollback()
	tx, err := suite.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	suite.Require().NoError(err)

	// Action
	rows, err := tx.QueryContext(context.Background(), query)
	suite.Require().NoError(err)
	rows.Close()
	suite.Require().NoError(tx.Rollback())

	// Assert
	suite.Equal(map[string]uint64{"ListOrders ok": 1}, suite.samples())
}

func (suite *TestDatabaseSuite) Test_SlowQueryLog() {
	// Arrange
	suite.db.in.slow = 10 * time.Millisecond
	suite.mock.ExpectExec("UPDATE orders SET status = $1").WithArgs("secret").
		WillDelayFor(20 * time.Millisecond).WillReturnResult(sqlmock.NewResult(0, 1))
	suite.mock.ExpectExec("SELECT 1").WillReturnResult(sqlmock.NewResult(0, 0))

	// Action
	_, err := suite.db.ExecContext(context.Background(), "UPDATE orders SET status = $1", "secret")
	suite.Require().NoError(err)
	_, err = suite.db.ExecContext(context.Background(), "SELECT 1")
	suite.Require().NoError(err)

	// Assert
	line := map[string]any{}
	suite.Require().NoError(json.Unmarshal(suite.logs.Bytes(), &line), "exactly one log line")
	suite.Equal("slow query", line["msg"])
	suite.Equal(unnamed, line["query"])
	suite.NotContains(suite.logs.String(), "secret")
	suite.NotContains(suite.logs.String(), "UPDATE")
}

func (suite *TestDatabaseSuite) Test_NilMetricClient() {
	// Arrange
	db := New(suite.db.DB, nil, 0)
	suite.mock.ExpectExec("SELECT 1").WillReturnResult(sqlmock.NewResult(0, 0))

	// Action
	_, err := db.ExecContext(context.Background(), "SELECT 1")

	// Assert
	suite.NoError(err)
}

type TestOpenSuite struct {
	suite.Suite
}

func TestTestOpenSuite(t *testing.T) {
	suite.Run(t, new(TestOpenSuite))
}

func (suite *TestOpenSuite) Test_Open() {
	// Arrange
	sqlDB, _, err := sqlmock.NewWithDSN("open_ok")
	suite.Require().NoError(err)
	defer sqlDB.Close()

	// Action
	db, err := Open(context.Background(), &Config{Driver: "sqlmock", DSN: "open_ok", MaxOpenConns: 5}, nil)

	// Assert
	suite.Require().NoError(err)
	defer db.Close()
	suite.Equal(5, db.Stats().MaxOpenConnections)
}

func (suite *TestOpenSuite) Test_Open_PingFails() {
	// Arrange
	sqlDB, mock, err := sqlmock.NewWithDSN("ping_fails", sqlmock.MonitorPingsOption(true))
	suite.Require().NoError(err)
	defer sqlDB.Close()
	mock.ExpectPing() // the ping from NewWithDSN's own pool
	mock.ExpectPing().WillReturnError(errors.New("connection refused"))
	suite.Require().NoError(sqlDB.Ping())

	// Action
	_, err = Open(context.Background(), &Config{Driver: "sqlmock", DSN: "ping_fails"}, nil)

	// Assert
	suite.ErrorContains(err, "database: ping: connection refused")
}

func (suite *TestOpenSuite) Test_Open_InvalidConfig() {
	for name, conf := range map[string]*Config{
		"nil":            nil,
		"no driver":      {DSN: "x"},
		"no dsn":         {Driver: "pgx"},
		"unknown driver": {Driver: "nope", DSN: "x"},
	} {
		suite.Run(name, func() {
			_, err := Open(context.Background(), conf, nil)
			suite.Error(err)
		})
	}
}

func (suite *TestOpenSuite) Test_Defaults() {
	c := Config{MaxOpenConns: 10}.withDefaults()
	suite.Equal(10, c.MaxIdleConns)
	suite.Equal(defaultConnMaxLifetime, c.ConnMaxLifetime)
	suite.Equal(defaultConnMaxIdleTime, c.ConnMaxIdleTime)
	suite.Equal(defaultPingTimeout, c.PingTimeout)
	suite.Equal(defaultMaxOpenConns, Config{}.withDefaults().MaxOpenConns)
}
