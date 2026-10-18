package metrics

import (
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Histogram interface {
	// Histogram set value for the histogram metric.
	Histogram(metricName string, value float64, tags ...Tag)
	// Duration set duration from start moment to the metric
	Duration(metricName string, start time.Time, tags ...Tag)
}

// histogram is used to bundle many instances of prometheus.Histogram.
type histogram struct {
	nameSpace string
	*collector
}

func newHistogram(register prometheus.Registerer, nameSpace string) *histogram {
	createHistogramFn := func(metricName string, tagKeys ...string) prometheus.Collector {
		return prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Namespace: nameSpace, Name: metricName},
			tagKeys,
		)
	}
	return &histogram{
		nameSpace: nameSpace,
		collector: newCollector(register, createHistogramFn),
	}
}

// Observe histogram with the value.
func (h *histogram) observe(metricName string, value float64, tags ...Tag) error {
	curHistogram, err := h.getOrCreateHistogram(h.nameSpace, metricName, tags...)
	if err != nil {
		return err
	}
	curHistogram.Observe(value)
	return nil
}

func (h *histogram) getOrCreateHistogram(nameSpace, metricName string, tags ...Tag) (prometheus.Observer, error) {
	curCollector := h.collector.GetOrCreateCollector(nameSpace, metricName, tags...)
	histogramVec, ok := curCollector.(*prometheus.HistogramVec)
	if !ok {
		err := errors.New("collector is not histogram vector")
		slog.Error(err.Error())
		return nil, err
	}

	tagMap := getTagMap(tags...)
	return histogramVec.GetMetricWith(tagMap)
}
