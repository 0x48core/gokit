package kafka

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/0x48core/gokit/logger"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

func TestBuildWithoutBootstrapServers(t *testing.T) {
	if _, err := NewProducerBuilder().Build(); !errors.Is(err, ErrNoBootstrapServers) {
		t.Fatalf("producer: got %v, want ErrNoBootstrapServers", err)
	}
	if _, err := NewConsumerBuilder(WithOthers(Params{"bootstrap.servers": 1})).Build(); !errors.Is(err, ErrNoBootstrapServers) {
		t.Fatalf("consumer: got %v, want ErrNoBootstrapServers", err)
	}
}

func TestBuilderOptions(t *testing.T) {
	producer := NewProducerBuilder(
		WithBootstrapServers("localhost:9092"),
		WithRequiredACKs("all"),
		WithAccount("user", "pass"),
		WithOthers(Params{"linger.ms": 5}),
	).WithIdempotency().WithCompression("lz4")

	assertParams(t, producer.ToKafkaConfigMap(), kafka.ConfigMap{
		"bootstrap.servers":  "localhost:9092",
		"acks":               "all",
		"sasl.username":      "user",
		"sasl.password":      "pass",
		"linger.ms":          5,
		"enable.idempotence": true,
		"compression.type":   "lz4",
	})

	consumer := NewConsumerBuilder(WithBootstrapServers("localhost:9092")).
		WithGroupID("orders").
		WithAutoResetOffset(Earliest).
		WithAutoCommit(false).
		WithAutoOffsetStore(false).
		WithQueuedMinMessages(10).
		WithQueuedMaxMessagesKBytes(2048).
		WithMaxPartitionFetchBytes(1024 * 1024).
		WithFetchMaxBytes(4 * 1024 * 1024).
		WithFetchQueueBackoffMs(50)

	assertParams(t, consumer.ToKafkaConfigMap(), kafka.ConfigMap{
		"bootstrap.servers":          "localhost:9092",
		"group.id":                   "orders",
		"auto.offset.reset":          "earliest",
		"enable.auto.commit":         false,
		"enable.auto.offset.store":   false,
		"queued.min.messages":        10,
		"queued.max.messages.kbytes": 2048,
		"max.partition.fetch.bytes":  1024 * 1024,
		"fetch.max.bytes":            4 * 1024 * 1024,
		"fetch.queue.backoff.ms":     50,
	})
}

func assertParams(t *testing.T, got, want kafka.ConfigMap) {
	t.Helper()
	if !maps.Equal(got, want) {
		t.Fatalf("params:\n got  %v\n want %v", got, want)
	}
}

func TestBatchStrategyRequiresManualCommit(t *testing.T) {
	builder := func(params Params) *ConsumerBuilder {
		return NewConsumerBuilder(WithBootstrapServers("localhost:9092"), WithOthers(params)).
			WithGroupID("orders").
			WithConsumeStrategy(NewBatchConsumeStrategy())
	}

	for name, params := range map[string]Params{
		"default":     {},
		"enabled":     {"enable.auto.commit": true},
		"string true": {"enable.auto.commit": "true"},
	} {
		if _, err := builder(params).Build(); !errors.Is(err, ErrBatchAutoCommit) {
			t.Errorf("%s: got %v, want ErrBatchAutoCommit", name, err)
		}
	}

	for name, params := range map[string]Params{
		"disabled":     {"enable.auto.commit": false},
		"string false": {"enable.auto.commit": "false"},
	} {
		consumer, err := builder(params).Build()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		_ = consumer.Close()
	}
}

func TestConsumerCloseWithoutStart(t *testing.T) {
	consumer, err := NewConsumerBuilder(WithBootstrapServers("localhost:9092")).WithGroupID("orders").Build()
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		consumer.Stop()
		_ = consumer.Close()
		_ = consumer.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close blocked on a consumer that was never started")
	}
}

func TestNextOffsetsUsesLatestOffsetPerPartition(t *testing.T) {
	topic := "orders"
	offsets := nextOffsets(
		&kafka.Message{TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: 0, Offset: 1}},
		&kafka.Message{TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: 0, Offset: 3}},
		&kafka.Message{TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: 1, Offset: 7}},
		nil,
		&kafka.Message{TopicPartition: kafka.TopicPartition{Partition: 9, Offset: 2}},
	)

	got := map[int32]kafka.Offset{}
	for _, tp := range offsets {
		got[tp.Partition] = tp.Offset
	}
	if want := map[int32]kafka.Offset{0: 4, 1: 8}; !maps.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTraceIDHeader(t *testing.T) {
	ctx := logger.WithTraceID(context.Background(), "abc123")
	msg := newMessage(ctx, "orders", []byte("k"), []byte("v"))

	if got := string(GetHeaderValue(msg.Headers, HeaderKeyTraceID)); got != "abc123" {
		t.Fatalf("header trace ID = %q, want abc123", got)
	}
	if got := logger.GetTraceID(ContextWithTraceID(context.Background(), msg)); got != "abc123" {
		t.Fatalf("context trace ID = %q, want abc123", got)
	}

	generated := newMessage(context.Background(), "orders", nil, nil)
	if len(GetHeaderValue(generated.Headers, HeaderKeyTraceID)) == 0 {
		t.Fatal("expected a generated trace ID when ctx has none")
	}
	if ctx := ContextWithTraceID(context.Background(), &kafka.Message{}); logger.GetTraceID(ctx) != "" {
		t.Fatal("expected no trace ID for a message without the header")
	}
}

func TestEnabled(t *testing.T) {
	cfg := kafka.ConfigMap{"a": false, "b": "false", "c": "yes", "d": 1}
	for key, want := range map[string]bool{"a": false, "b": false, "c": true, "d": true, "missing": true} {
		if got := enabled(cfg, key); got != want {
			t.Errorf("%s: got %v, want %v", key, got, want)
		}
	}
}
