package metrics

import (
	"errors"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

type Gauge interface {
	// Gauge set value for gauge
	Gauge(metricName string, value float64, tags ...Tag)
	// GaugeInc increments value of gauge by 1.
	GaugeInc(metricName string, tags ...Tag)
	// GaugeDec decrements value of gauge by 1.
	GaugeDec(metricName string, tags ...Tag)
}

// gauge is used to bundle many instances of prometheus.Gauge.
type gauge struct {
	nameSpace string
	*collector
}

func newGauge(register prometheus.Registerer, nameSpace string) *gauge {
	createGaugeFn := func(metricName string, tagKeys ...string) prometheus.Collector {
		return prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: nameSpace,
				Name:      metricName,
			}, tagKeys,
		)
	}

	return &gauge{
		nameSpace: nameSpace,
		collector: newCollector(register, createGaugeFn),
	}
}

// Set gauge with the value.
func (g *gauge) Set(metricName string, value float64, tags ...Tag) error {
	gauge, err := g.getOrCreateGauge(g.nameSpace, metricName, tags...)
	if err != nil {
		return err
	}
	gauge.Set(value)
	return nil
}

// Inc increases gauge by 1.
func (g *gauge) Inc(metricName string, tags ...Tag) error {
	gauge, err := g.getOrCreateGauge(g.nameSpace, metricName, tags...)
	if err != nil {
		return err
	}
	gauge.Inc()
	return nil
}

// Dec decreases gauge by 1.
func (g *gauge) Dec(metricName string, tags ...Tag) error {
	gauge, err := g.getOrCreateGauge(g.nameSpace, metricName, tags...)
	if err != nil {
		return err
	}
	gauge.Dec()
	return nil
}

func (g *gauge) getOrCreateGauge(nameSpace, metricName string, tags ...Tag) (prometheus.Gauge, error) {
	collector := g.collector.GetOrCreateCollector(nameSpace, metricName, tags...)
	gaugeVec, ok := collector.(*prometheus.GaugeVec)
	if !ok {
		err := errors.New("collector is not gauge vector")
		slog.Error(err.Error())
		return nil, err
	}

	tagMap := getTagMap(tags...)
	return gaugeVec.GetMetricWith(tagMap)
}
