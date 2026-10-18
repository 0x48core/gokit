# gokit

A collection of small, reusable Go packages for building services.

Each package is its own Go module, so you only pull in the dependencies of the packages you use.

## Packages

| Package | Description | Install |
| --- | --- | --- |
| [logger](./logger) | Structured JSON logging with `log/slog`, with trace IDs and key/values carried in `context.Context`. | `go get github.com/0x48core/gokit/logger` |
| [metrics](./metrics) | Counters, gauges and histograms on top of the Prometheus Go client, created and registered on first use. | `go get github.com/0x48core/gokit/metrics` |
| [snowflake](./snowflake) | Unique, time-ordered 64-bit IDs, with node IDs leased from MySQL, PostgreSQL or MongoDB. | `go get github.com/0x48core/gokit/snowflake` |

See each package's README for usage and API details.

## Requirements

Go 1.25 or later.

## Development

Each package has its own `Makefile`. Run the targets from the package directory:

```sh
cd logger
make fmt   # goimports, gofumpt, gci
make lint  # golangci-lint + go vet
make test  # go test with race detector and coverage
```

## License

[Apache License 2.0](./LICENSE)
