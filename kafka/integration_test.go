package kafka

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

// Integration tests run against the brokers in KAFKA_BROKERS, for example
// "127.0.0.1:39092". They are skipped when it is unset.
func brokers(t *testing.T) string {
	t.Helper()
	servers := os.Getenv("KAFKA_BROKERS")
	if servers == "" {
		t.Skip("KAFKA_BROKERS not set")
	}
	return servers
}

func newTopic(t *testing.T, servers string, partitions int) string {
	t.Helper()
	topic := fmt.Sprintf("gokit_test_%d", time.Now().UnixNano())

	admin, err := kafka.NewAdminClient(&kafka.ConfigMap{"bootstrap.servers": servers})
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results, err := admin.CreateTopics(ctx, []kafka.TopicSpecification{
		{Topic: topic, NumPartitions: partitions, ReplicationFactor: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := results[0].Error; err.Code() != kafka.ErrNoError {
		t.Fatal(err)
	}
	return topic
}

func produceAll(t *testing.T, servers, topic string, n int) {
	t.Helper()
	producer, err := NewProducerBuilder(WithBootstrapServers(servers)).WithIdempotency().WithCompression("lz4").Build()
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()

	for i := range n {
		if _, err := producer.Produce(context.Background(), topic, nil, fmt.Appendf(nil, "%d", i)); err != nil {
			t.Fatal(err)
		}
	}
}

// collector records the values of every message the handler receives.
type collector struct {
	mu      sync.Mutex
	values  []string
	batches int
}

func (c *collector) handle(messages ...*kafka.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range messages {
		c.values = append(c.values, string(m.Value))
	}
	c.batches++
	return nil
}

func (c *collector) waitFor(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		if len(c.values) >= n {
			values := slices.Clone(c.values)
			c.mu.Unlock()
			slices.Sort(values)
			return values
		}
		c.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("received %d messages, want %d", len(c.values), n)
	return nil
}

func wantValues(n int) []string {
	values := make([]string, n)
	for i := range n {
		values[i] = fmt.Sprintf("%d", i)
	}
	slices.Sort(values)
	return values
}

func committedTotal(t *testing.T, consumer *Consumer, topic string, partitions int) kafka.Offset {
	t.Helper()
	tps := make([]kafka.TopicPartition, partitions)
	for i := range tps {
		tps[i] = kafka.TopicPartition{Topic: &topic, Partition: int32(i)}
	}
	committed, err := consumer.consumer.Committed(tps, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var total kafka.Offset
	for _, tp := range committed {
		if tp.Offset >= 0 {
			total += tp.Offset
		}
	}
	return total
}

func TestProduceAndConsume(t *testing.T) {
	servers := brokers(t)
	topic := newTopic(t, servers, 3)
	const total = 50
	produceAll(t, servers, topic, total)

	got := &collector{}
	consumer, err := NewConsumerBuilder(WithBootstrapServers(servers)).
		WithGroupID(topic).
		WithAutoResetOffset(Earliest).
		WithMessageHandler(got.handle, nil).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = consumer.Close() }()
	if err := consumer.Subscribe(topic); err != nil {
		t.Fatal(err)
	}
	consumer.Start()

	if values := got.waitFor(t, total); !slices.Equal(values, wantValues(total)) {
		t.Fatalf("got %v", values)
	}
}

func TestBatchConsumeCommitsAfterEachBatch(t *testing.T) {
	servers := brokers(t)
	const partitions, total, batchSize = 3, 100, 10
	topic := newTopic(t, servers, partitions)
	produceAll(t, servers, topic, total)

	got := &collector{}
	consumer, err := NewConsumerBuilder(WithBootstrapServers(servers)).
		WithGroupID(topic).
		WithAutoResetOffset(Earliest).
		WithAutoCommit(false).
		WithConsumeStrategy(NewBatchConsumeStrategy()).
		WithBatchSize(batchSize).
		WithBatchLinger(200*time.Millisecond).
		WithMessageHandler(got.handle, nil).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = consumer.Close() }()
	if err := consumer.Subscribe(topic); err != nil {
		t.Fatal(err)
	}
	consumer.Start()

	if values := got.waitFor(t, total); !slices.Equal(values, wantValues(total)) {
		t.Fatalf("got %v", values)
	}
	consumer.Stop()

	got.mu.Lock()
	batches := got.batches
	got.mu.Unlock()
	if batches > total/batchSize+partitions*2 {
		t.Errorf("handler ran %d times for %d messages; batching not applied", batches, total)
	}
	if committed := committedTotal(t, consumer, topic, partitions); committed != total {
		t.Errorf("committed offsets sum to %d, want %d", committed, total)
	}
}
