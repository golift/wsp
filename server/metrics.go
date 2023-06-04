package server

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics contains the exported prometheus metrics used by the application.
type Metrics struct {
	Pools     prometheus.Gauge
	Conns     prometheus.Gauge
	Busy      prometheus.Gauge
	Idle      prometheus.Gauge
	Closed    prometheus.Gauge
	Regs      prometheus.Counter
	RegFail   prometheus.Counter
	ReqStatus *prometheus.CounterVec
	ReqTime   prometheus.Histogram
	Uptime    prometheus.CounterFunc
}

func getMetrics() *Metrics {
	start := time.Now()

	return &Metrics{
		Pools: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "mulery_pools",
			Help: "The total count of active pools",
		}),
		Conns: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "mulery_connections",
			Help: "The total count of websocket connections",
		}),
		Busy: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "mulery_connections_busy",
			Help: "The total count of active websocket connections",
		}),
		Idle: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "mulery_connections_idle",
			Help: "The total count of idle websocket connections",
		}),
		Closed: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "mulery_connections_closed",
			Help: "The total count of disconnected websocket connections",
		}),
		Regs: promauto.NewCounter(prometheus.CounterOpts{
			Name: "mulery_registrations_total",
			Help: "The total count of websocket registrations",
		}),
		RegFail: promauto.NewCounter(prometheus.CounterOpts{
			Name: "mulery_registrations_failed_total",
			Help: "The total count of websocket registrations that failed (auth problem)",
		}),
		ReqStatus: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "mulery_http_request_statuses_total",
			Help: "The status codes of ->client requests",
		}, []string{"code"}),
		ReqTime: promauto.NewHistogram(prometheus.HistogramOpts{
			Name:    "mulery_http_request_time_seconds",
			Help:    "The duration of ->client requests",
			Buckets: []float64{.1, .5, 1, 3, 10, 30, 60, 180, 600},
		}),
		Uptime: promauto.NewCounterFunc(prometheus.CounterOpts{
			Name: "mulery_uptime_seconds_total",
			Help: "Seconds Mulery has been running",
		}, func() float64 { return time.Since(start).Seconds() }),
	}
}
