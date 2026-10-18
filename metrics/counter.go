package metrics

import (
	"errors"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

type Counter interface {
	// CountInc increase the metric by 1.
	CountInc(metricName string, tags ...Tag)
	// Count increase the metric by amount.
	Count(metricName string, amount int64, tags ...Tag)
}

// counter is used to bundle many instances of prometheus.Counter.
type counter struct {
	nameSpace string
	*collector
}

func newCounter(register prometheus.Registerer, nameSpace string) *counter {
	createCounterFn := func(metricName string, tagKeys ...string) prometheus.Collector {
		return prometheus.NewCounterVec(
			prometheus.CounterOpts{Namespace: nameSpace, Name: metricName},
			tagKeys,
		)
	}

	return &counter{
		nameSpace: nameSpace,
		collector: newCollector(register, createCounterFn),
	}
}

// Add to counter with amount value.
func (c *counter) add(metricName string, amount int64, tags ...Tag) error {
	counter, err := c.getOrCreateCounter(c.nameSpace, metricName, tags...)
	if err != nil {
		return err
	}
	counter.Add(float64(amount))
	return nil
}

func (c *counter) getOrCreateCounter(serviceName, metricName string, tags ...Tag) (prometheus.Counter, error) {
	collector := c.collector.GetOrCreateCollector(serviceName, metricName, tags...)
	counterVec, ok := collector.(*prometheus.CounterVec)
	if !ok {
		err := errors.New("collector is not counter vector")
		slog.Error(err.Error())
		return nil, err
	}

	tagMap := getTagMap(tags...)
	return counterVec.GetMetricWith(tagMap)
}
