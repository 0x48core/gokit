package kafka

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

const (
	defaultPollTimeoutMs = 100
	defaultBatchSize     = 16
	defaultBatchLinger   = 500 * time.Millisecond
)

// ErrBatchAutoCommit is returned by Build when the batch strategy is used with
// auto-commit enabled.
var ErrBatchAutoCommit = errors.New("kafka: batch consume strategy must disable auto commit")

type ConsumerBuilder struct {
	*baseBuilder

	pollTimeoutMs        int
	enableLogIgnoreEvent bool

	batchSize   int
	batchLinger time.Duration

	consumeStrategy    *ConsumeStrategy
	messageHandler     MessageHandler
	deadMessageHandler MessageHandler
	errorHandler       ErrorHandler
}

func NewConsumerBuilder(opts ...BuilderOption) *ConsumerBuilder {
	return &ConsumerBuilder{
		baseBuilder: newBaseBuilder(opts),

		pollTimeoutMs:      defaultPollTimeoutMs,
		batchSize:          defaultBatchSize,
		batchLinger:        defaultBatchLinger,
		consumeStrategy:    NewSingleConsumeStrategy(),
		messageHandler:     DefaultMessageHandler,
		deadMessageHandler: DefaultMessageHandler,
		errorHandler:       DefaultErrorHandler,
	}
}

// WithAutoCommit periodically commits the offset of the last message handed to
// the application. Default: true.
func (b *ConsumerBuilder) WithAutoCommit(autoCommit bool) *ConsumerBuilder {
	b.Params["enable.auto.commit"] = autoCommit
	return b
}

func (b *ConsumerBuilder) WithGroupID(groupID string) *ConsumerBuilder {
	b.Params["group.id"] = groupID
	return b
}

type AutoResetOffsetSetting string

func (s AutoResetOffsetSetting) String() string {
	return string(s)
}

const (
	Earliest AutoResetOffsetSetting = "earliest"
	Latest   AutoResetOffsetSetting = "latest"
)

// WithAutoResetOffset sets where to start when the group has no committed offset
// or it is out of range: Earliest or Latest.
func (b *ConsumerBuilder) WithAutoResetOffset(mode AutoResetOffsetSetting) *ConsumerBuilder {
	b.Params["auto.offset.reset"] = mode.String()
	return b
}

// WithAutoOffsetStore controls whether offsets are stored when a message is
// delivered to the application. Disable it for long-running handlers so offsets
// are only stored after the handler returns.
func (b *ConsumerBuilder) WithAutoOffsetStore(autoOffsetStore bool) *ConsumerBuilder {
	b.Params["enable.auto.offset.store"] = autoOffsetStore
	return b
}

// WithQueuedMinMessages sets the minimum number of messages librdkafka tries to
// keep prefetched in the local queue.
func (b *ConsumerBuilder) WithQueuedMinMessages(queuedMinMessages int) *ConsumerBuilder {
	b.Params["queued.min.messages"] = queuedMinMessages
	return b
}

// WithQueuedMaxMessagesKBytes bounds the local consumer queue in KiB.
func (b *ConsumerBuilder) WithQueuedMaxMessagesKBytes(queuedMaxMessagesKBytes int) *ConsumerBuilder {
	b.Params["queued.max.messages.kbytes"] = queuedMaxMessagesKBytes
	return b
}

// WithMaxPartitionFetchBytes sets the initial maximum bytes fetched per partition.
func (b *ConsumerBuilder) WithMaxPartitionFetchBytes(maxPartitionFetchBytes int) *ConsumerBuilder {
	b.Params["max.partition.fetch.bytes"] = maxPartitionFetchBytes
	return b
}

// WithFetchMaxBytes sets the maximum bytes returned by a single fetch request.
func (b *ConsumerBuilder) WithFetchMaxBytes(fetchMaxBytes int) *ConsumerBuilder {
	b.Params["fetch.max.bytes"] = fetchMaxBytes
	return b
}

// WithFetchQueueBackoffMs sets how long librdkafka waits before fetching again
// once the local queue limits are reached.
func (b *ConsumerBuilder) WithFetchQueueBackoffMs(fetchQueueBackoffMs int) *ConsumerBuilder {
	b.Params["fetch.queue.backoff.ms"] = fetchQueueBackoffMs
	return b
}

// WithPollTimeoutMs sets how long each poll blocks waiting for an event.
func (b *ConsumerBuilder) WithPollTimeoutMs(pollTimeoutMs int) *ConsumerBuilder {
	b.pollTimeoutMs = pollTimeoutMs
	return b
}

// WithLogIgnoreEvent logs events the consumer does not handle, such as partition EOF.
func (b *ConsumerBuilder) WithLogIgnoreEvent(enable bool) *ConsumerBuilder {
	b.enableLogIgnoreEvent = enable
	return b
}

// WithBatchSize runs the handler once this many messages are buffered.
// Only used by the batch strategy.
func (b *ConsumerBuilder) WithBatchSize(batchSize int) *ConsumerBuilder {
	b.batchSize = batchSize
	return b
}

// WithBatchLinger runs the handler on a partial batch once this long has passed
// since the last run. Only used by the batch strategy.
func (b *ConsumerBuilder) WithBatchLinger(batchLinger time.Duration) *ConsumerBuilder {
	b.batchLinger = batchLinger
	return b
}

// WithConsumeStrategy sets how messages reach the handler: NewSingleConsumeStrategy
// (default) or NewBatchConsumeStrategy. Use a new strategy for each consumer.
func (b *ConsumerBuilder) WithConsumeStrategy(strategy *ConsumeStrategy) *ConsumerBuilder {
	b.consumeStrategy = strategy
	return b
}

// WithMessageHandler sets the handler, and the dead message handler that receives
// the messages when the handler returns an error.
func (b *ConsumerBuilder) WithMessageHandler(msgHandler MessageHandler, deadMsgHandler MessageHandler) *ConsumerBuilder {
	b.messageHandler = msgHandler
	b.deadMessageHandler = deadMsgHandler
	return b
}

func (b *ConsumerBuilder) WithErrorHandler(errorHandler ErrorHandler) *ConsumerBuilder {
	b.errorHandler = errorHandler
	return b
}

func (b *ConsumerBuilder) Build() (*Consumer, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}

	kafkaConfigMap := b.ToKafkaConfigMap()
	kafkaConfigMap["go.application.rebalance.enable"] = false // librdkafka handles rebalances
	kafkaConfigMap["enable.partition.eof"] = true             // emit an event at the end of each partition

	if err := b.consumeStrategy.VerifyConfig(kafkaConfigMap); err != nil {
		return nil, err
	}

	consumer, err := kafka.NewConsumer(&kafkaConfigMap)
	if err != nil {
		return nil, fmt.Errorf("kafka: create consumer: %w", err)
	}

	return &Consumer{
		kafkaConfigMap: kafkaConfigMap,
		consumerConfig: &consumerConfig{
			pollTimeoutMs:        b.pollTimeoutMs,
			enableLogIgnoreEvent: b.enableLogIgnoreEvent,
			batchSize:            b.batchSize,
			batchLinger:          b.batchLinger,
		},
		consumer: consumer,

		consumeStrategy:    b.consumeStrategy,
		messageHandler:     b.messageHandler,
		deadMessageHandler: b.deadMessageHandler,
		errorHandler:       b.errorHandler,
		stop:               make(chan struct{}),
		completeStop:       make(chan struct{}),
	}, nil
}

type consumerConfig struct {
	pollTimeoutMs        int
	enableLogIgnoreEvent bool
	batchSize            int
	batchLinger          time.Duration
}

type Consumer struct {
	kafkaConfigMap kafka.ConfigMap
	consumerConfig *consumerConfig
	consumer       *kafka.Consumer

	consumeStrategy    *ConsumeStrategy
	messageHandler     MessageHandler
	deadMessageHandler MessageHandler
	errorHandler       ErrorHandler

	started      atomic.Bool
	stopOnce     sync.Once
	closeOnce    sync.Once
	closeErr     error
	stop         chan struct{}
	completeStop chan struct{}
}

func (c *Consumer) Subscribe(topics ...string) error {
	return c.consumer.SubscribeTopics(topics, nil)
}

func (c *Consumer) Unsubscribe() error {
	return c.consumer.Unsubscribe()
}

// Start polls in a background goroutine until Stop or Close. Calling it again has no effect.
func (c *Consumer) Start() {
	if !c.started.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer close(c.completeStop)
		for {
			select {
			case <-c.stop:
				slog.Info("Consumer stopping...")
				_ = c.Unsubscribe()
				return
			default:
				ev := c.consumer.Poll(c.consumerConfig.pollTimeoutMs)
				c.consumeStrategy.Consume(c, ev)
			}
		}
	}()
}

// Stop ends polling and waits for the handler in progress to return. Messages
// buffered by the batch strategy are not handled or committed, so they are
// delivered again later.
func (c *Consumer) Stop() {
	c.stopOnce.Do(func() { close(c.stop) })
	if c.started.Load() {
		<-c.completeStop
	}
}

// Close stops the consumer and leaves the group. It is safe to call more than once.
func (c *Consumer) Close() error {
	c.closeOnce.Do(func() {
		c.Stop()
		c.closeErr = c.consumer.Close()
	})
	return c.closeErr
}

func (c *Consumer) handle(messages ...*kafka.Message) {
	if err := c.messageHandler(messages...); err != nil && c.deadMessageHandler != nil {
		_ = c.deadMessageHandler(messages...)
	}
	c.afterMessagesProcessed(messages...)
}

func (c *Consumer) afterMessagesProcessed(messages ...*kafka.Message) {
	if len(messages) == 0 {
		return
	}

	if enabled(c.kafkaConfigMap, "enable.auto.commit") {
		if !enabled(c.kafkaConfigMap, "enable.auto.offset.store") {
			c.storeProcessedOffsets(messages...)
		}
		return
	}

	offsets := nextOffsets(messages...)
	if len(offsets) == 0 {
		return
	}
	if _, err := c.consumer.CommitOffsets(offsets); err != nil {
		slog.Error("Failed to commit offsets", "error", err)
	}
}

func (c *Consumer) storeProcessedOffsets(messages ...*kafka.Message) {
	offsets := nextOffsets(messages...)
	if len(offsets) == 0 {
		return
	}
	if _, err := c.consumer.StoreOffsets(offsets); err != nil {
		slog.Error("Failed to store offsets", "error", err)
	}
}

func (c *Consumer) logIgnoredEvent(event kafka.Event) {
	if event != nil && c.consumerConfig.enableLogIgnoreEvent {
		slog.Info("Ignored event", slog.Any("event", event))
	}
}

type topicPartitionKey struct {
	topic     string
	partition int32
}

// nextOffsets returns, for each partition in messages, the offset after its
// latest message: the offset to commit once all of them are processed.
func nextOffsets(messages ...*kafka.Message) []kafka.TopicPartition {
	latestOffsets := make(map[topicPartitionKey]kafka.TopicPartition, len(messages))

	for _, msg := range messages {
		if msg == nil || msg.TopicPartition.Topic == nil {
			continue
		}

		tp := msg.TopicPartition
		key := topicPartitionKey{topic: *tp.Topic, partition: tp.Partition}
		offset := kafka.TopicPartition{Topic: tp.Topic, Partition: tp.Partition, Offset: tp.Offset + 1}
		if current, ok := latestOffsets[key]; !ok || offset.Offset > current.Offset {
			latestOffsets[key] = offset
		}
	}

	offsets := make([]kafka.TopicPartition, 0, len(latestOffsets))
	for _, offset := range latestOffsets {
		offsets = append(offsets, offset)
	}
	return offsets
}

// MessageHandler handles one message, or a batch with the batch strategy.
// The slice is reused after it returns, so copy it to keep it.
type (
	MessageHandler func(messages ...*kafka.Message) error
	ErrorHandler   func(err kafka.Error)
)

var (
	DefaultMessageHandler MessageHandler = func(messages ...*kafka.Message) error {
		slog.Info("Receive message", slog.Any("messages", messages))
		return nil
	}
	DefaultErrorHandler ErrorHandler = func(err kafka.Error) {
		slog.Error("Receive error", "error", err)
	}
)

// ConsumeStrategy decides when polled messages are passed to the handler.
type ConsumeStrategy struct {
	Consume      func(c *Consumer, event kafka.Event)
	VerifyConfig func(cfg kafka.ConfigMap) error
}

// NewSingleConsumeStrategy passes each message to the handler as it arrives.
func NewSingleConsumeStrategy() *ConsumeStrategy {
	return &ConsumeStrategy{
		Consume: func(c *Consumer, event kafka.Event) {
			switch e := event.(type) {
			case *kafka.Message:
				c.handle(e)
			case kafka.Error:
				c.errorHandler(e)
			default:
				c.logIgnoredEvent(e)
			}
		},
		VerifyConfig: func(kafka.ConfigMap) error {
			return nil
		},
	}
}

// NewBatchConsumeStrategy buffers messages and passes them to the handler once
// the batch size is reached or the batch linger has passed. Offsets are committed
// after each batch, so auto-commit must be disabled. It keeps state, so each
// consumer needs its own strategy.
func NewBatchConsumeStrategy() *ConsumeStrategy {
	var messages []*kafka.Message
	lastExecuteTime := time.Now()

	executeBatch := func(c *Consumer) {
		ready := len(messages) >= c.consumerConfig.batchSize ||
			(len(messages) > 0 && time.Since(lastExecuteTime) >= c.consumerConfig.batchLinger)
		if !ready {
			return
		}

		c.handle(messages...)
		messages = messages[:0] // keep allocated memory
		lastExecuteTime = time.Now()
	}

	return &ConsumeStrategy{
		Consume: func(c *Consumer, event kafka.Event) {
			switch e := event.(type) {
			case *kafka.Message:
				messages = append(messages, e)
				executeBatch(c)
			case kafka.Error:
				c.errorHandler(e)
			default:
				c.logIgnoredEvent(e)
				executeBatch(c)
			}
		},
		VerifyConfig: func(cfg kafka.ConfigMap) error {
			if enabled(cfg, "enable.auto.commit") {
				return ErrBatchAutoCommit
			}
			return nil
		},
	}
}
