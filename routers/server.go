package routers

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	promMetrics "github.com/slok/go-http-metrics/metrics/prometheus"

	"github.com/zapier/prom-aggregation-gateway/metrics"
)

func RunServers(cfg ApiRouterConfig, apiListen string, lifecycleListen string) {
	sigChannel := make(chan os.Signal, 1)
	signal.Notify(sigChannel, syscall.SIGTERM, syscall.SIGINT)

	var opts []metrics.AggregateOptionsFunc

	ignoredLabels := os.Getenv("IGNORED_LABELS")
	if len(ignoredLabels) != 0 {
		opts = append(opts, metrics.AddIgnoredLabels(strings.Split(ignoredLabels, ",")...))
	}

	metricTTL := os.Getenv("METRIC_TTL")
	if len(metricTTL) > 0 {
		parsed, err := time.ParseDuration(metricTTL)
		if err == nil {
			opts = append(opts, metrics.SetTTLMetricTime(&parsed))
		} else {
			fmt.Printf("can not parse duration. METRIC_TTL err: %v\n", err)
		}
	}

	aggregateDelay := os.Getenv("AGGREGATE_DELAY")
	if len(aggregateDelay) > 0 {
		parsed, err := time.ParseDuration(aggregateDelay)
		if err == nil {
			opts = append(opts, metrics.SetAggregateDelay(parsed))
		} else {
			fmt.Printf("can not parse duration. AGGREGATE_DELAY err: %v\n", err)
		}
	}

	agg := metrics.NewAggregate(opts...)

	promMetricsConfig := promMetrics.Config{
		Registry: metrics.PromRegistry,
	}

	apiRouter := setupAPIRouter(cfg, agg, promMetricsConfig)
	go runServer("api", apiRouter, apiListen)

	lifecycleRouter := setupLifecycleRouter(metrics.PromRegistry)
	go runServer("lifecycle", lifecycleRouter, lifecycleListen)

	// Block until an interrupt or term signal is sent
	<-sigChannel
}

func runServer(label string, r *gin.Engine, listen string) {
	log.Printf("%s server listening at %s", label, listen)
	if err := r.Run(listen); err != nil {
		log.Panicf("error while serving %s: %v", label, err)
	}
}
