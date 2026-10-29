# Database

A thin layer over `database/sql` for PostgreSQL, MySQL or any other driver, with pool defaults,
a transaction helper, per-query latency metrics and slow query logs.

- `DB` is a `*sql.DB` and `Tx` is a `*sql.Tx`, with the same method signatures, so
  [sqlc](https://sqlc.dev)-generated code and anything else written for `database/sql` uses them
  as-is
- Each query's latency recorded in a `database` histogram from [metrics](../metrics), tagged with
  the query's name, never its SQL
- Query names read from sqlc's `-- name: GetOrder :one` comments, or set with `WithQueryName`
- `WithTx` commits on success and rolls back on error or panic
- Slow queries logged without their SQL or arguments, which may hold personal data
- No driver dependency: import the one you use

## Installation

```sh
go get github.com/0x48core/gokit/database
```

Requires Go 1.25+.

## Usage

```go
import (
	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"; or _ "github.com/go-sql-driver/mysql" for "mysql"

	"github.com/0x48core/gokit/database"
)

db, err := database.Open(ctx, &database.Config{
	Driver:             "pgx",
	DSN:                "postgres://app:secret@localhost:5432/orders",
	MaxOpenConns:       20,
	SlowQueryThreshold: 500 * time.Millisecond,
}, metricClient)
if err != nil {
	return err
}
defer db.Close()
```

`Open` returns an error if the database doesn't answer a ping within `PingTimeout`. To wrap a
pool you opened yourself, use `database.New(sqlDB, metricClient, slowQueryThreshold)`.

### With sqlc

sqlc's generated `New(db DBTX)` takes a `*database.DB` or a `*database.Tx`, and each query is
named after its `-- name:` comment:

```go
queries := store.New(db)
order, err := queries.GetOrder(ctx, id) // recorded as query="GetOrder"
```

### Without sqlc

Name a query through its context, or it is recorded as `unnamed`:

```go
ctx = database.WithQueryName(ctx, "orders.count_pending")
err := db.QueryRowContext(ctx, "SELECT count(*) FROM orders WHERE status = $1", "pending").Scan(&n)
```

### Transactions

```go
err := db.WithTx(ctx, nil, func(ctx context.Context, tx *database.Tx) error {
	q := store.New(tx)
	if err := q.ReserveStock(ctx, itemID); err != nil {
		return err // rolls back
	}
	return q.CreateOrder(ctx, params) // commits if nil
})
```

`WithTx` rolls back when `fn` returns an error or panics, and re-panics after the rollback. Use
the `ctx` and `tx` passed to `fn` for every query inside. Pass `&sql.TxOptions{...}` to set the
isolation level or make the transaction read-only.

### Config

`Config` has `mapstructure` tags, so it loads with [config](../config). Zero fields get the
defaults:

| Field | Key | Default |
| --- | --- | --- |
| `Driver` | `driver` | required, such as `pgx` or `mysql` |
| `DSN` | `dsn` | required |
| `MaxOpenConns` | `max_open_conns` | `25` |
| `MaxIdleConns` | `max_idle_conns` | `MaxOpenConns` |
| `ConnMaxLifetime` | `conn_max_lifetime` | `30m` |
| `ConnMaxIdleTime` | `conn_max_idle_time` | `5m` |
| `PingTimeout` | `ping_timeout` | `5s` |
| `SlowQueryThreshold` | `slow_query_threshold` | `0`, no slow query logs |

`database/sql`'s own default is no limit on open connections and only 2 idle ones, which can
overload the database under load and reconnect constantly.

### Metrics and logs

Each `ExecContext`, `QueryContext` and `QueryRowContext` call on `DB` or `Tx` records its latency
in seconds in `<namespace>_database`, tagged `query` and `status` (`ok` or `error`). For
`QueryContext`, that is the time until the first rows are ready, not the time spent reading
them.

Queries slower than `SlowQueryThreshold` log `slow query` at warn level with the query name and
`duration_ms`, using the query's context, so the [logger](../logger) trace ID is included.

## API

| Name | Description |
| --- | --- |
| `Open(ctx, conf, metricClient)` | Opens a pool with `conf`'s settings and pings it. `metricClient` may be `nil`. |
| `New(sqlDB, metricClient, slowQueryThreshold)` | Wraps an existing `*sql.DB`. |
| `(*DB).WithTx(ctx, opts, fn)` | Runs `fn` in a transaction. |
| `(*DB).BeginTx(ctx, opts)` | Starts a transaction and returns a `*Tx`. |
| `WithQueryName(ctx, name)` | Names the queries run with `ctx`. Takes precedence over sqlc comments. |
| `DBTX` | The query interface `DB` and `Tx` share, matching sqlc's. |

## Notes

- **Only the context methods are recorded.** `Exec`, `Query`, `QueryRow` and statements from
  `PrepareContext` work but record nothing. Use the `...Context` methods, as sqlc does.
- **Use a fixed set of query names.** Each name is a metric series, so don't build names from
  IDs or user input.
- **Health checks:** `db.PingContext` fits [health](../health)`.WithCheck` as-is.

## Development

```sh
make fmt   # goimports, gofumpt, gci
make lint  # golangci-lint + go vet
make test  # go test with race detector and coverage
```
