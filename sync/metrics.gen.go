// Code generated by metricsgen. DO NOT EDIT.

package sync

import (
	"github.com/go-kit/kit/metrics/discard"
	prometheus "github.com/go-kit/kit/metrics/prometheus"
	stdprometheus "github.com/prometheus/client_golang/prometheus"
)

func PrometheusMetrics(namespace string, labelsAndValues ...string) *Metrics {
	labels := []string{}
	for i := 0; i < len(labelsAndValues); i += 2 {
		labels = append(labels, labelsAndValues[i])
	}
	return &Metrics{
		SyncedL1Height: prometheus.NewGaugeFrom(stdprometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: MetricsSubsystem,
			Name:      "l1height",
			Help:      "",
		}, labels).With(labelsAndValues...),
		SyncedL1MessageNonce: prometheus.NewGaugeFrom(stdprometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: MetricsSubsystem,
			Name:      "message_nonce",
			Help:      "",
		}, labels).With(labelsAndValues...),
		SyncedL1MessageCount: prometheus.NewCounterFrom(stdprometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: MetricsSubsystem,
			Name:      "message_count",
			Help:      "",
		}, labels).With(labelsAndValues...),
	}
}

func NopMetrics() *Metrics {
	return &Metrics{
		SyncedL1Height:       discard.NewGauge(),
		SyncedL1MessageNonce: discard.NewGauge(),
		SyncedL1MessageCount: discard.NewCounter(),
	}
}
