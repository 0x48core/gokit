package metrics

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cast"
)

// Tag describe some tags relative with the metrics.
type Tag struct {
	Key   string
	Value string
}

func getTagMap(tags ...Tag) map[string]string {
	tagMap := make(map[string]string)
	for _, tag := range tags {
		tagMap[tag.Key] = tag.Value
	}
	return tagMap
}

const (
	prometheusKeySeparator = "_"
)

func buildHashKey(serviceName, metricName string) string {
	var hashKey strings.Builder

	if len(serviceName) > 0 {
		hashKey.WriteString(serviceName)
		hashKey.WriteString(prometheusKeySeparator)
	}

	hashKey.WriteString(metricName)

	return hashKey.String()
}

func getSortedTagKeys(tags ...Tag) []string {
	tagKeys := make([]string, 0, len(tags))
	for _, tag := range tags {
		tagKeys = append(tagKeys, tag.Key)
	}
	sort.Strings(tagKeys)
	return tagKeys
}

func MonitoringTimed(metricClient Client, metricsName string) func(tagsArgs ...interface{}) {
	start := time.Now()
	return func(tagsArgs ...interface{}) {
		argsLen := len(tagsArgs)
		if argsLen%2 != 0 {
			slog.Warn("bad args, missing key")
			return
		}
		tagIdx := 0
		tags := make([]Tag, len(tagsArgs)/2)
		for i := 0; i < argsLen; i += 2 {
			tags[tagIdx] = Tag{
				Key:   cast.ToString(tagsArgs[i]),
				Value: cast.ToString(tagsArgs[i+1]),
			}
			tagIdx++
		}
		duration := time.Since(start).Seconds()
		if metricClient == nil {
			slog.Error(fmt.Sprintf("invalid metric client, metricName=%v, tags=%v", metricsName, tags))
		}
		metricClient.Histogram(metricsName, duration, tags...)
	}
}
