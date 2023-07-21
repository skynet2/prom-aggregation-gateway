package metrics

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/gin-gonic/gin"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

type metricFamily struct {
	*dto.MetricFamily
	lock sync.RWMutex
}

type Aggregate struct {
	familiesLock sync.RWMutex
	families     map[string]*metricFamily
	options      aggregateOptions
}

type ignoredLabels []string

type aggregateOptions struct {
	ignoredLabels     ignoredLabels
	metricTTLDuration *time.Duration
	aggregateDeley    time.Duration
}

type AggregateOptionsFunc func(a *Aggregate)

func AddIgnoredLabels(ignoredLabels ...string) AggregateOptionsFunc {
	return func(a *Aggregate) {
		a.options.ignoredLabels = ignoredLabels
	}
}

func SetTTLMetricTime(duration *time.Duration) AggregateOptionsFunc {
	return func(a *Aggregate) {
		a.options.metricTTLDuration = duration
	}
}

func SetAggregateDelay(duration time.Duration) AggregateOptionsFunc {
	return func(a *Aggregate) {
		a.options.aggregateDeley = duration
	}
}

func NewAggregate(opts ...AggregateOptionsFunc) *Aggregate {
	a := &Aggregate{
		families: map[string]*metricFamily{},
		options: aggregateOptions{
			ignoredLabels:  []string{},
			aggregateDeley: 10 * time.Second,
		},
	}

	for _, opt := range opts {
		opt(a)
	}

	a.options.formatOptions()

	fmt.Println("aggregate options")
	fmt.Println(spew.Sdump(a.options))
	return a
}

func (ao *aggregateOptions) formatOptions() {
	ao.formatIgnoredLabels()
}

func (ao *aggregateOptions) formatIgnoredLabels() {
	if ao.ignoredLabels != nil {
		for i, v := range ao.ignoredLabels {
			ao.ignoredLabels[i] = strings.ToLower(v)
		}
	}

	sort.Strings(ao.ignoredLabels)
}

func (a *Aggregate) Len() int {
	a.familiesLock.RLock()
	count := len(a.families)
	a.familiesLock.RUnlock()
	return count
}

// setFamilyOrGetExistingFamily either sets a new family or returns an existing family
func (a *Aggregate) setFamilyOrGetExistingFamily(familyName string, family *dto.MetricFamily) *metricFamily {
	a.familiesLock.Lock()
	defer a.familiesLock.Unlock()
	existingFamily, ok := a.families[familyName]
	if !ok {
		a.families[familyName] = &metricFamily{MetricFamily: family}
		return nil
	}
	return existingFamily
}

func (a *Aggregate) saveFamily(familyName string, family *dto.MetricFamily) error {
	existingFamily := a.setFamilyOrGetExistingFamily(familyName, family)
	if existingFamily != nil {
		err := existingFamily.mergeFamily(family)
		if err != nil {
			return err
		}
	}

	return nil
}

func (a *Aggregate) parseAndMerge(r io.Reader, labels []labelPair) error {
	var parser expfmt.TextParser
	inFamilies, err := parser.TextToMetricFamilies(r)
	if err != nil {
		return err
	}

	for name, family := range inFamilies {
		if len(family.Metric) == 0 {
			continue
		}
		// Sort labels in case source sends them inconsistently
		for _, m := range family.Metric {
			a.formatLabels(m, labels)
		}

		if err := validateFamily(family); err != nil {
			return err
		}

		// family must be sorted for the merge
		sort.Sort(byLabel(family.Metric))

		if err := a.saveFamily(name, family); err != nil {
			return err
		}

		MetricCountByFamily.WithLabelValues(name).Set(float64(len(family.Metric)))

	}

	TotalFamiliesGauge.Set(float64(a.Len()))

	return nil
}

func (a *Aggregate) HandleRender(c *gin.Context) {
	contentType := expfmt.Negotiate(c.Request.Header)
	c.Header("Content-Type", string(contentType))
	a.encodeAllMetrics(c.Writer, contentType)

	// TODO reset gauges
}

func (a *Aggregate) encodeAllMetrics(writer io.Writer, contentType expfmt.Format) {
	enc := expfmt.NewEncoder(writer, contentType)

	a.familiesLock.RLock()
	defer a.familiesLock.RUnlock()

	metricNames := []string{}
	metricTypeCounts := make(map[string]int)
	for name, family := range a.families {
		if len(family.Metric) == 0 {
			continue
		}

		metricNames = append(metricNames, name)
		var typeName string
		if family.Type == nil {
			typeName = "unknown"
		} else {
			typeName = dto.MetricType_name[int32(*family.Type)]
		}
		metricTypeCounts[typeName]++
	}

	sort.Strings(metricNames)

	for _, name := range metricNames {
		if a.encodeMetric(name, enc) {
			continue
		}
	}

	MetricCountByType.Reset()
	for typeName, count := range metricTypeCounts {
		MetricCountByType.WithLabelValues(typeName).Set(float64(count))
	}

}

func (a *Aggregate) encodeMetric(name string, enc expfmt.Encoder) bool {
	a.families[name].lock.RLock()
	defer a.families[name].lock.RUnlock()

	if err := enc.Encode(a.families[name].MetricFamily); err != nil {
		log.Printf("An error has occurred during metrics encoding:\n\n%s\n", err.Error())
		return true
	}
	return false
}

var ErrOddNumberOfLabelParts = errors.New("labels must be defined in pairs")

func (a *Aggregate) HandleInsert(c *gin.Context) {
	labelString := c.Param("labels")
	bodyData, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Println(err)
		http.Error(c.Writer, err.Error(), http.StatusBadRequest)
	}

	c.Status(http.StatusAccepted)
	go func() {
		time.Sleep(a.options.aggregateDeley)
		labelParts, jobName, err := parseLabelsInPath(labelString)
		if err != nil {
			log.Println(err)
			return
		}

		if err := a.parseAndMerge(bytes.NewBuffer(bodyData), labelParts); err != nil {
			return
		}

		MetricPushes.WithLabelValues(jobName).Inc()
	}()
}

type labelPair struct {
	name, value string
}

func parseLabelsInPath(labelString string) ([]labelPair, string, error) {
	labelString = strings.Trim(labelString, "/")
	if labelString == "" {
		return nil, "", nil
	}

	labelParts := strings.Split(labelString, "/")
	if len(labelParts)%2 != 0 {
		return nil, "", ErrOddNumberOfLabelParts
	}

	var (
		labelPairs []labelPair
		jobName    string
	)
	for idx := 0; idx < len(labelParts); idx += 2 {
		name := labelParts[idx]
		value := labelParts[idx+1]
		labelPairs = append(labelPairs, labelPair{name, value})
		if name == "job" {
			jobName = value
		}
	}

	return labelPairs, jobName, nil
}
