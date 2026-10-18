# Metrics

A small wrapper around the [Prometheus Go client](https://github.com/prometheus/client_golang) for counters, gauges and histograms.

- Metrics are created and registered on first use, so there's no setup per metric
- Every metric is prefixed with a namespace, for example `myapp_http_requests_total`
- An `env` label is added to every metric automatically
- Safe for concurrent use

## Installation

```sh
go get github.com/0x48core/gokit/metrics
```

## Usage

```go
package main

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/0x48core/gokit/metrics"
)

func main() {
	// Registers metrics on prometheus.DefaultRegisterer.
	client := metrics.NewClient("myapp", "production")

	http.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer client.Duration("http_request_duration_seconds", start,
			metrics.Tag{Key: "path", Value: "/hello"})

		client.CountInc("http_requests_total", metrics.Tag{Key: "path", Value: "/hello"})
		w.Write([]byte("hello"))
	})

	// Expose metrics for Prometheus to scrape.
	http.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(":8080", nil)
}
```

`GET /metrics` then returns, among others:

```text
myapp_http_requests_total{env="production",path="/hello"} 1
myapp_http_request_duration_seconds_bucket{env="production",path="/hello",le="0.005"} 1
...
```

### Custom registry

Use `NewClientWithRegisterer` to register metrics somewhere other than the default registry, for example in tests:

```go
reg := prometheus.NewRegistry()
client := metrics.NewClientWithRegisterer("myapp", "test", reg)
```

### Timing a function

`MonitoringTimed` starts a timer and returns a function that records the elapsed seconds in a histogram. Labels are passed as key/value pairs:

```go
func process() (err error) {
	done := metrics.MonitoringTimed(client, "process_duration_seconds")
	defer func() { done("status", statusOf(err)) }()
	// ...
}
```

## API

### Client

| Function | Description |
| --- | --- |
| `NewClient(namespace, environment)` | Creates a client that registers on `prometheus.DefaultRegisterer`. |
| `NewClientWithRegisterer(namespace, environment, registerer)` | Creates a client that registers on the given `prometheus.Registerer`. |

### Counter

| Method | Description |
| --- | --- |
| `CountInc(name, tags...)` | Increases the counter by 1. |
| `Count(name, amount, tags...)` | Increases the counter by `amount`. |

### Gauge

| Method | Description |
| --- | --- |
| `Gauge(name, value, tags...)` | Sets the gauge to `value`. |
| `GaugeInc(name, tags...)` | Increases the gauge by 1. |
| `GaugeDec(name, tags...)` | Decreases the gauge by 1. |

### Histogram

| Method | Description |
| --- | --- |
| `Histogram(name, value, tags...)` | Records `value`. |
| `Duration(name, start, tags...)` | Records the seconds elapsed since `start`. |
| `MonitoringTimed(client, name)` | Returns a function that records the seconds elapsed since the call. Takes labels as `key, value, ...`. |

Histograms use the Prometheus default buckets (`prometheus.DefBuckets`, 5ms to 10s).

## Notes

- **Use the same label keys every time.** The label keys from the first call define the metric. A later call with different keys is dropped and logs no error.
- **Don't use `env` as a label key.** The client adds it to every metric, so passing your own `env` tag duplicates the label.
- **Errors are not returned.** Client methods don't return errors, so a failure (for example a wrong label set) is ignored.
- **Name collisions panic.** Each metric type registers its own collector, so using the same name for a counter and a gauge panics on registration.

## Development

```sh
make fmt   # goimports, gofumpt, gci
make lint  # golangci-lint + go vet
make test  # go test with race detector and coverage
```
