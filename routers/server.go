package routers

import (
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

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
