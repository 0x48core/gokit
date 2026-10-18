# Snowflake

Unique, roughly time-ordered 64-bit IDs, in the style of Twitter's Snowflake.

```text
 0 | 41 bits: ms since epoch | 10 bits: node ID | 12 bits: sequence
```

- About 69 years of IDs from the epoch (default `2026-01-01 UTC`)
- Up to 1024 nodes, each generating up to 4096 IDs per millisecond
- IDs are positive `int64`s and increase over time on each node
- Uses the monotonic clock, so wall clock adjustments can't make IDs go backwards
- Node IDs can be assigned automatically from MySQL, PostgreSQL or MongoDB

## Installation

```sh
go get github.com/0x48core/gokit/snowflake
```

Requires Go 1.25+.

## Usage

### Fixed node ID

When each process already has a unique number (for example a StatefulSet ordinal):

```go
node, err := snowflake.New(3)
if err != nil {
	return err
}

id, err := node.Generate()
```

### Node ID from a database

Every running process needs a different node ID. A registry leases one from a shared
database table and keeps the lease alive in the background:

```go
import (
	"database/sql"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/0x48core/gokit/snowflake"
	"github.com/0x48core/gokit/snowflake/postgres"
)

db, err := sql.Open("pgx", "postgres://user:pass@localhost:5432/app")
if err != nil {
	return err
}

registry, err := postgres.New(db)
if err != nil {
	return err
}

node, err := snowflake.NewWithRegistry(ctx, registry)
if err != nil {
	return err
}
defer registry.Release(context.Background())

id, err := node.Generate()
```

The MySQL and MongoDB registries work the same way:

```go
registry, err := mysql.New(db) // db opened with github.com/go-sql-driver/mysql
registry, err := mongodb.New(client.Database("app").Collection(mongodb.DefaultCollection))
```

### Parsing an ID

```go
parts := node.Parse(id) // or snowflake.Parse(id, snowflake.DefaultEpoch)
fmt.Println(parts.Time, parts.NodeID, parts.Sequence)
```

## How node ID leasing works

1. On first use, the registry creates a table (or collection) with one row per node ID, 0 to 1023.
2. `Acquire` takes the lowest node ID whose lease has expired and sets it to expire after the TTL (default one minute).
3. A background goroutine renews the lease every TTL/3.
4. `Release` frees the node ID right away. If a process crashes instead, its node ID becomes free once the lease expires.

All expiry times come from the database clock, so clock differences between hosts don't matter.

If renewal fails twice in a row, or another process has taken the node ID, the lease counts as lost. `Generate` then returns `ErrLeaseLost` instead of risking duplicate IDs. Watch `registry.Lost()` to restart or acquire a new node ID.

| Backend | Package | Default table / collection | Tested with |
| --- | --- | --- | --- |
| MySQL | `snowflake/mysql` | `snowflake_nodes` | MySQL 8.4 |
| PostgreSQL | `snowflake/postgres` | `snowflake_nodes` | PostgreSQL 17 (needs 9.5+) |
| MongoDB | `snowflake/mongodb` | your choice, e.g. `snowflake_nodes` | MongoDB 7 (needs 4.2+) |

Registry options: `WithTTL`, `WithOwner`, and `WithTable` (MySQL and PostgreSQL).

To use another store, implement `snowflake.NodeRegistry`:

```go
type NodeRegistry interface {
	Acquire(ctx context.Context) (int64, error)
	Release(ctx context.Context) error
	Lost() <-chan struct{} // nil if the node ID can never be lost
}
```

## API

| Name | Description |
| --- | --- |
| `New(nodeID, opts...)` | Creates a Node with a fixed node ID in `[0, MaxNodeID]`. |
| `NewWithRegistry(ctx, registry, opts...)` | Acquires a node ID from the registry and creates a Node with it. |
| `WithEpoch(t)` | Sets the epoch. Every node sharing an ID space must use the same one. |
| `(*Node).Generate()` | Returns a new ID. Waits for the next millisecond if 4096 IDs were already generated in this one. |
| `(*Node).Parse(id)`, `Parse(id, epoch)` | Splits an ID into `Time`, `NodeID` and `Sequence`. |
| `ErrLeaseLost` | Returned by `Generate` after the registry lost the node ID. |
| `ErrNoNodeIDAvailable` | Returned by a registry when all 1024 node IDs are leased. |
| `ErrTimeOverflow` | Returned once the 41-bit time range is used up (about 69 years after the epoch). |

## Development

```sh
make fmt               # goimports, gofumpt, gci
make lint              # golangci-lint + go vet
make test              # unit tests; registry tests are skipped
make test-integration  # all tests against MySQL, PostgreSQL and MongoDB in Docker
```

The registry tests read `SNOWFLAKE_MYSQL_DSN`, `SNOWFLAKE_POSTGRES_DSN` and `SNOWFLAKE_MONGODB_URI`, and skip any backend whose variable is unset.
