package observability

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

var histogramBounds = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30}

type metricKey struct {
	name  string
	attrs string
}

type histogramValue struct {
	count   uint64
	sum     float64
	buckets []uint64
}

type metricSeries struct {
	counter    float64
	counterSet bool
	histogram  histogramValue
	gauge      float64
	gaugeSet   bool
}

type metricStore struct {
	mu     sync.RWMutex
	series map[metricKey]*metricSeries
}

func newMetricStore() *metricStore {
	return &metricStore{series: make(map[metricKey]*metricSeries)}
}

func metricKeyFor(name string, attrs []Attribute) metricKey {
	parts := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		parts = append(parts, attr.Key+"="+formatAttributeValue(attr))
	}
	sort.Strings(parts)
	return metricKey{name: name, attrs: strings.Join(parts, "\x1f")}
}

func formatAttributeValue(attr Attribute) string {
	switch attr.Kind {
	case attributeInt:
		return strconv.FormatInt(attr.Int, 10)
	case attributeFloat:
		return strconv.FormatFloat(attr.Float, 'g', -1, 64)
	case attributeBool:
		return strconv.FormatBool(attr.Bool)
	default:
		return attr.Text
	}
}

func (s *metricStore) entry(key metricKey) *metricSeries {
	series, ok := s.series[key]
	if !ok {
		series = &metricSeries{histogram: histogramValue{buckets: make([]uint64, len(histogramBounds))}}
		s.series[key] = series
	}
	return series
}

func (s *metricStore) inc(name string, attrs []Attribute, value float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	series := s.entry(metricKeyFor(name, attrs))
	series.counter += value
	series.counterSet = true
}

func (s *metricStore) observe(name string, attrs []Attribute, value float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	histogram := &s.entry(metricKeyFor(name, attrs)).histogram
	histogram.count++
	histogram.sum += value
	for index, bound := range histogramBounds {
		if value <= bound {
			histogram.buckets[index]++
		}
	}
}

func (s *metricStore) setGauge(name string, attrs []Attribute, value float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	series := s.entry(metricKeyFor(name, attrs))
	series.gauge = value
	series.gaugeSet = true
}

func escapeLabel(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n")
	return replacer.Replace(sanitiseValue(value))
}

func labelBlock(key metricKey) string {
	if key.attrs == "" {
		return ""
	}
	parts := strings.Split(key.attrs, "\x1f")
	var b strings.Builder
	b.WriteByte('{')
	for index, part := range parts {
		name, value, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		if index > 0 {
			b.WriteByte(',')
		}
		b.WriteString(escapeLabel(name))
		b.WriteString("=\"")
		b.WriteString(escapeLabel(value))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func (s *metricStore) render() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]metricKey, 0, len(s.series))
	for key := range s.series {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name != keys[j].name {
			return keys[i].name < keys[j].name
		}
		return keys[i].attrs < keys[j].attrs
	})
	var out strings.Builder
	for _, key := range keys {
		series := s.series[key]
		labels := labelBlock(key)
		if series.counterSet {
			out.WriteString("# TYPE ")
			out.WriteString(key.name)
			out.WriteString(" counter\n")
			out.WriteString(key.name)
			out.WriteString(labels)
			out.WriteByte(' ')
			out.WriteString(strconv.FormatFloat(series.counter, 'g', -1, 64))
			out.WriteByte('\n')
		}
		if series.gaugeSet {
			out.WriteString("# TYPE ")
			out.WriteString(key.name)
			out.WriteString(" gauge\n")
			out.WriteString(key.name)
			out.WriteString(labels)
			out.WriteByte(' ')
			out.WriteString(strconv.FormatFloat(series.gauge, 'g', -1, 64))
			out.WriteByte('\n')
		}

		histogram := series.histogram
		if histogram.count == 0 {
			continue
		}
		out.WriteString("# TYPE ")
		out.WriteString(key.name)
		out.WriteString(" histogram\n")
		bucketLabels := func(bound string) string {
			if key.attrs == "" {
				return "{le=\"" + bound + "\"}"
			}
			return strings.TrimSuffix(labels, "}") + ",le=\"" + bound + "\"}"
		}
		var cumulative uint64
		for index, bound := range histogramBounds {
			cumulative += histogram.buckets[index]
			out.WriteString(key.name)
			out.WriteString("_bucket")
			out.WriteString(bucketLabels(strconv.FormatFloat(bound, 'g', -1, 64)))
			out.WriteByte(' ')
			out.WriteString(strconv.FormatUint(cumulative, 10))
			out.WriteByte('\n')
		}
		out.WriteString(key.name)
		out.WriteString("_bucket")
		out.WriteString(bucketLabels("+Inf"))
		out.WriteByte(' ')
		out.WriteString(strconv.FormatUint(histogram.count, 10))
		out.WriteByte('\n')
		out.WriteString(key.name)
		out.WriteString("_sum")
		out.WriteString(labels)
		out.WriteByte(' ')
		out.WriteString(strconv.FormatFloat(histogram.sum, 'g', -1, 64))
		out.WriteByte('\n')
		out.WriteString(key.name)
		out.WriteString("_count")
		out.WriteString(labels)
		out.WriteByte(' ')
		out.WriteString(strconv.FormatUint(histogram.count, 10))
		out.WriteByte('\n')
	}
	return out.String()
}
