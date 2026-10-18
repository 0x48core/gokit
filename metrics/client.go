package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const tagEnv = "env"

type Client interface {
	Gauge
	Counter
	Histogram
}

type client struct {
	registerer  prometheus.Registerer
	environment string
	counter     *counter
	histogram   *histogram
	gauge       *gauge
}

// NewClient creates new prometheusClient instance with default registerer (prometheus.DefaultRegisterer).
func NewClient(namespace string, environment string) Client {
	return NewClientWithRegisterer(namespace, environment, prometheus.DefaultRegisterer)
}

// NewClientWithRegisterer creates new prometheusClient.
func NewClientWithRegisterer(namespace string, environment string, registerer prometheus.Registerer) Client {
	return &client{
		registerer:  registerer,
		environment: environment,
		counter:     newCounter(registerer, namespace),
		histogram:   newHistogram(registerer, namespace),
		gauge:       newGauge(registerer, namespace),
	}
}

func (c *client) addDefaultTags(tags ...Tag) []Tag {
	return append(tags, Tag{Key: tagEnv, Value: c.environment})
}

func (c *client) CountInc(metricName string, tags ...Tag) {
	tags = c.addDefaultTags(tags...)
	_ = c.counter.add(metricName, 1, tags...)
}

func (c *client) Count(metricName string, amount int64, tags ...Tag) {
	tags = c.addDefaultTags(tags...)
	_ = c.counter.add(metricName, amount, tags...)
}

func (c *client) Histogram(metricName string, value float64, tags ...Tag) {
	tags = c.addDefaultTags(tags...)
	_ = c.histogram.observe(metricName, value, tags...)
}

func (c *client) Duration(metricName string, start time.Time, tags ...Tag) {
	tags = c.addDefaultTags(tags...)
	elapsed := float64(time.Since(start)) / float64(time.Second)
	_ = c.histogram.observe(metricName, elapsed, tags...)
}

func (c *client) Gauge(metricName string, value float64, tags ...Tag) {
	tags = c.addDefaultTags(tags...)
	_ = c.gauge.Set(metricName, value, tags...)
}

func (c *client) GaugeInc(metricName string, tags ...Tag) {
	tags = c.addDefaultTags(tags...)
	_ = c.gauge.Inc(metricName, tags...)
}

func (c *client) GaugeDec(metricName string, tags ...Tag) {
	tags = c.addDefaultTags(tags...)
	_ = c.gauge.Dec(metricName, tags...)
}
