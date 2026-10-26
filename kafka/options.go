package kafka

import (
	"errors"
	"maps"
	"strconv"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

// ErrNoBootstrapServers is returned by Build when bootstrap.servers is not set.
var ErrNoBootstrapServers = errors.New("kafka: bootstrap.servers must not be empty")

// Params holds librdkafka configuration properties:
// https://github.com/confluentinc/librdkafka/blob/master/CONFIGURATION.md
type Params kafka.ConfigMap

type baseBuilder struct {
	Params
}

type BuilderOption func(builder *baseBuilder)

func newBaseBuilder(opts []BuilderOption) *baseBuilder {
	base := &baseBuilder{Params: Params{}}
	for _, opt := range opts {
		opt(base)
	}
	return base
}

// ToKafkaConfigMap returns a copy of the configured properties.
func (b *baseBuilder) ToKafkaConfigMap() kafka.ConfigMap {
	dst := kafka.ConfigMap{}
	maps.Copy(dst, b.Params)
	return dst
}

func (b *baseBuilder) Validate() error {
	if servers, _ := b.Params["bootstrap.servers"].(string); servers == "" {
		return ErrNoBootstrapServers
	}
	return nil
}

func WithBootstrapServers(servers string) BuilderOption {
	return func(builder *baseBuilder) {
		builder.Params["bootstrap.servers"] = servers
	}
}

// WithRequiredACKs sets acks, the number of in-sync replicas that must acknowledge
// a produce request: 0 (no ack), 1 (leader only) or -1/all (every in-sync replica).
// Default: all.
func WithRequiredACKs(requiredACKs string) BuilderOption {
	return func(builder *baseBuilder) {
		builder.Params["acks"] = requiredACKs
	}
}

func WithSecurityProtocol(protocol string) BuilderOption {
	return func(builder *baseBuilder) {
		builder.Params["security.protocol"] = protocol
	}
}

func WithSASLMechanisms(mechanisms string) BuilderOption {
	return func(builder *baseBuilder) {
		builder.Params["sasl.mechanisms"] = mechanisms
	}
}

func WithKerberos(serviceName, principal, keytab string) BuilderOption {
	return func(builder *baseBuilder) {
		maps.Copy(builder.Params, Params{
			"sasl.kerberos.service.name": serviceName,
			"sasl.kerberos.principal":    principal,
			"sasl.kerberos.keytab":       keytab,
		})
	}
}

func WithAccount(username, password string) BuilderOption {
	return func(builder *baseBuilder) {
		maps.Copy(builder.Params, Params{
			"sasl.username": username,
			"sasl.password": password,
		})
	}
}

// WithOthers sets any other librdkafka properties.
func WithOthers(params Params) BuilderOption {
	return func(builder *baseBuilder) {
		maps.Copy(builder.Params, params)
	}
}

// enabled reports whether a boolean property that defaults to true is still on.
// The property may have been set as a bool or a string.
func enabled(cfg kafka.ConfigMap, key string) bool {
	switch v := cfg[key].(type) {
	case bool:
		return v
	case string:
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return true
}
