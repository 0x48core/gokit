package kafka

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/0x48core/gokit/logger"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

// HeaderKeyTraceID is the message header that carries the trace ID.
const HeaderKeyTraceID = "trace_id"

type ProducerBuilder struct {
	*baseBuilder
}

func NewProducerBuilder(opts ...BuilderOption) *ProducerBuilder {
	return &ProducerBuilder{baseBuilder: newBaseBuilder(opts)}
}

// WithIdempotency makes the producer write each message exactly once and in order.
// librdkafka then defaults to max.in.flight.requests.per.connection=5,
// retries=INT32_MAX, acks=all and queuing.strategy=fifo, and Build fails if the
// configuration conflicts with those.
func (b *ProducerBuilder) WithIdempotency() *ProducerBuilder {
	b.Params["enable.idempotence"] = true
	return b
}

// WithCompression sets compression.type: none, gzip, snappy, lz4 or zstd.
func (b *ProducerBuilder) WithCompression(compressionType string) *ProducerBuilder {
	b.Params["compression.type"] = compressionType
	return b
}

func (b *ProducerBuilder) Build() (*Producer, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}

	cfg := b.ToKafkaConfigMap()
	producer, err := kafka.NewProducer(&cfg)
	if err != nil {
		return nil, fmt.Errorf("kafka: create producer: %w", err)
	}
	return &Producer{producer: producer}, nil
}

type Producer struct {
	producer *kafka.Producer
}

// Produce writes a message to topic and waits for the broker to acknowledge it.
// The trace ID from ctx (or a new one) is sent in the trace_id header.
func (p *Producer) Produce(ctx context.Context, topic string, key, value []byte) (*kafka.Message, error) {
	deliveryChan := make(chan kafka.Event, 1)
	if err := p.producer.Produce(newMessage(ctx, topic, key, value), deliveryChan); err != nil {
		slog.ErrorContext(ctx, "Failed to produce message", "topic", topic, "error", err)
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case event := <-deliveryChan:
		msg, ok := event.(*kafka.Message)
		if !ok {
			return nil, fmt.Errorf("kafka: unexpected delivery event %v", event)
		}
		if err := msg.TopicPartition.Error; err != nil {
			slog.ErrorContext(ctx, "Delivery failed", "topic", topic, "error", err)
			return nil, err
		}
		return msg, nil
	}
}

// AsyncProduce queues a message without waiting for delivery. The delivery report
// is sent to deliveryChan, or to Events() when deliveryChan is nil.
func (p *Producer) AsyncProduce(ctx context.Context, topic string, key, value []byte, deliveryChan chan kafka.Event) error {
	if err := p.producer.Produce(newMessage(ctx, topic, key, value), deliveryChan); err != nil {
		slog.ErrorContext(ctx, "Failed to produce message", "topic", topic, "error", err)
		return err
	}
	return nil
}

// Events returns delivery reports for messages produced without a delivery channel.
func (p *Producer) Events() chan kafka.Event {
	return p.producer.Events()
}

// Flush waits up to timeout for queued messages to be delivered and returns
// the number still queued.
func (p *Producer) Flush(timeout time.Duration) int {
	return p.producer.Flush(int(timeout.Milliseconds()))
}

// Close closes the producer without waiting for queued messages; call Flush first.
func (p *Producer) Close() {
	p.producer.Close()
}

func newMessage(ctx context.Context, topic string, key, value []byte) *kafka.Message {
	return &kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &topic,
			Partition: kafka.PartitionAny,
		},
		Key:       key,
		Value:     value,
		Timestamp: time.Now().UTC(),
		Headers:   []kafka.Header{{Key: HeaderKeyTraceID, Value: []byte(traceID(ctx))}},
	}
}

func traceID(ctx context.Context) string {
	if id := logger.GetTraceID(ctx); id != "" {
		return id
	}
	_, id := logger.GenerateTraceID(ctx)
	return id
}

// GetHeaderValue returns the value of the first header named key, or nil.
func GetHeaderValue(headers []kafka.Header, key string) []byte {
	for _, h := range headers {
		if h.Key == key {
			return h.Value
		}
	}
	return nil
}

// ContextWithTraceID returns ctx carrying the trace ID from msg's trace_id header,
// so logs written while handling msg share the producer's trace ID.
func ContextWithTraceID(ctx context.Context, msg *kafka.Message) context.Context {
	if msg == nil {
		return ctx
	}
	if id := GetHeaderValue(msg.Headers, HeaderKeyTraceID); len(id) > 0 {
		return logger.WithTraceID(ctx, string(id))
	}
	return ctx
}
