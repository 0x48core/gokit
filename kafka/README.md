# Kafka

Kafka producer and consumer built on [confluent-kafka-go](https://github.com/confluentinc/confluent-kafka-go) (librdkafka).

- Builder-style configuration, with an escape hatch for any librdkafka property
- Synchronous and asynchronous produce, with the trace ID from [logger](../logger) sent in a `trace_id` header
- Consumers that pass messages to a handler one at a time or in batches
- Offsets committed or stored only after the handler returns, so messages are processed at least once

## Installation

```sh
go get github.com/0x48core/gokit/kafka
```

Requires Go 1.25+ and cgo. confluent-kafka-go bundles a prebuilt librdkafka for Linux (glibc and musl) and macOS.
On other platforms, or to use your own build, install librdkafka and build with `-tags dynamic`:

```sh
brew install librdkafka pkg-config
```

## Usage

### Producer

```go
producer, err := kafka.NewProducerBuilder(
	kafka.WithBootstrapServers("localhost:9092"),
	kafka.WithRequiredACKs("all"),
).
	WithIdempotency().
	WithCompression("lz4").
	Build()
if err != nil {
	return err
}
defer producer.Close()

// Waits for the broker to acknowledge the message.
msg, err := producer.Produce(ctx, "orders", []byte("order-1"), payload)

// Returns once the message is queued. Delivery reports go to the channel,
// or to producer.Events() when it is nil.
err = producer.AsyncProduce(ctx, "orders", []byte("order-1"), payload, nil)

// Before shutting down, wait for queued messages.
producer.Flush(10 * time.Second)
```

### Consumer

```go
consumer, err := kafka.NewConsumerBuilder(
	kafka.WithBootstrapServers("localhost:9092"),
).
	WithGroupID("orders").
	WithAutoResetOffset(kafka.Earliest).
	WithMessageHandler(handle, deadLetter).
	Build()
if err != nil {
	return err
}
defer consumer.Close()

if err := consumer.Subscribe("orders"); err != nil {
	return err
}
consumer.Start() // polls in the background until Stop or Close
```

```go
func handle(messages ...*ckafka.Message) error {
	for _, msg := range messages {
		ctx := kafka.ContextWithTraceID(context.Background(), msg) // logs share the producer's trace ID
		slog.InfoContext(ctx, "order received", "key", string(msg.Key))
	}
	return nil
}
```

`ckafka` is `github.com/confluentinc/confluent-kafka-go/v2/kafka`. When the handler returns an error,
the same messages are passed to the dead message handler (the second argument; `nil` to skip), and
their offsets are still committed.

### Batch consumer

The batch strategy calls the handler once `WithBatchSize` messages are buffered, or once
`WithBatchLinger` has passed with a partial batch. It commits offsets after each batch, so it
requires auto-commit to be off:

```go
consumer, err := kafka.NewConsumerBuilder(
	kafka.WithBootstrapServers("localhost:9092"),
).
	WithGroupID("orders").
	WithAutoCommit(false).
	WithConsumeStrategy(kafka.NewBatchConsumeStrategy()). // one strategy per consumer
	WithBatchSize(100).
	WithBatchLinger(time.Second).
	WithMessageHandler(handle, nil).
	Build()
```

The slice passed to the handler is reused for the next batch, so copy it if you keep it.

## Offsets

| Settings | When offsets advance |
| --- | --- |
| Default (auto-commit on) | librdkafka stores each offset when the message is polled and commits periodically. |
| `WithAutoOffsetStore(false)` | Offsets are stored after the handler returns, then committed periodically. Use for slow handlers. |
| `WithAutoCommit(false)` | Offsets are committed after the handler returns. Required by the batch strategy. |

`Stop` waits for the handler in progress to finish. Messages buffered by the batch strategy that
were not handled yet are not committed, so they are delivered again after a restart.

## Consumer memory tuning

librdkafka prefetches messages into native memory, outside the Go heap. Its defaults favour
throughput (`queued.min.messages=100000`, `queued.max.messages.kbytes=65536`,
`fetch.max.bytes=52428800`), and every consumer in a process keeps its own queue. With large
payloads, slow handlers or several consumers per pod, this can cause OOM kills while the Go heap
looks small. Lower the limits:

```go
kafka.NewConsumerBuilder(kafka.WithBootstrapServers("localhost:9092")).
	WithGroupID("orders").
	WithQueuedMinMessages(10).
	WithQueuedMaxMessagesKBytes(2048).
	WithMaxPartitionFetchBytes(1024 * 1024).
	WithFetchMaxBytes(4 * 1024 * 1024).
	WithFetchQueueBackoffMs(50)
```

Set `max.partition.fetch.bytes` just above your largest message or compressed batch, and keep
`fetch.max.bytes` small enough that one fetch can't exhaust the pod's memory.

## API

| Name | Description |
| --- | --- |
| `WithBootstrapServers`, `WithRequiredACKs`, `WithSecurityProtocol`, `WithSASLMechanisms`, `WithAccount`, `WithKerberos` | Options shared by producers and consumers. |
| `WithOthers(Params)` | Sets any other [librdkafka property](https://github.com/confluentinc/librdkafka/blob/master/CONFIGURATION.md). |
| `NewProducerBuilder(opts...)` | `WithIdempotency`, `WithCompression`, then `Build`. |
| `(*Producer).Produce`, `AsyncProduce`, `Events`, `Flush`, `Close` | Send messages and wait for delivery. |
| `NewConsumerBuilder(opts...)` | `WithGroupID`, `WithAutoResetOffset`, `WithAutoCommit`, `WithAutoOffsetStore`, `WithMessageHandler`, `WithErrorHandler`, `WithConsumeStrategy`, `WithBatchSize`, `WithBatchLinger`, `WithPollTimeoutMs`, `WithLogIgnoreEvent`, memory tuning options, then `Build`. |
| `(*Consumer).Subscribe`, `Start`, `Stop`, `Close` | Consume until stopped. `Close` is safe to call more than once. |
| `NewSingleConsumeStrategy`, `NewBatchConsumeStrategy` | How messages reach the handler. |
| `ContextWithTraceID(ctx, msg)`, `GetHeaderValue(headers, key)` | Read the `trace_id` header, or any other header. |
| `ErrNoBootstrapServers`, `ErrBatchAutoCommit` | Returned by `Build` for invalid configuration. |

## Development

```sh
make fmt               # goimports, gofumpt, gci
make lint              # golangci-lint + go vet
make test              # unit tests; broker tests are skipped
make test-integration  # all tests against a single-node Kafka broker in Docker
```

The broker tests read `KAFKA_BROKERS` and are skipped when it is unset.
