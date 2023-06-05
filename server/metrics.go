package server

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics contains the exported application metrics in prometheus format.
type Metrics struct {
	Uptime    prometheus.CounterFunc
	Pools     prometheus.Gauge
	Conns     *prometheus.GaugeVec
	Regs      *prometheus.CounterVec
	PoolConns *prometheus.GaugeVec
	reqStatus *prometheus.CounterVec
	reqTime   *prometheus.HistogramVec
}

func getMetrics() *Metrics {
	start := time.Now()

	return &Metrics{
		Uptime: promauto.NewCounterFunc(prometheus.CounterOpts{
			Name: "mulery_uptime_seconds_total",
			Help: "Seconds Mulery has been running",
		}, func() float64 { return time.Since(start).Seconds() }),
		Pools: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "mulery_pools",
			Help: "The total count of active pools",
		}),
		Conns: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mulery_connections",
			Help: "The gauges for websocket connection statuses",
		}, []string{"state"}),
		Regs: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "mulery_registrations_total",
			Help: "The counters for websocket registration statuses",
		}, []string{"state"}),
		reqStatus: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "mulery_http_request_statuses_total",
			Help: "The status codes of ->client requests",
		}, []string{"code", "method"}),
		PoolConns: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mulery_pools_by_count_of_connections",
			Help: "Pools with N connections",
		}, []string{"connections"}),
		reqTime: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "mulery_http_request_time_seconds",
			Help:    "Duration of ->client HTTP requests",
			Buckets: []float64{.1, .5, 1, 3, 10, 30, 60, 180, 600},
		}, []string{"code", "method", "handler"}),
	}
}

func (m *Metrics) Wrap(next http.HandlerFunc, handler string) http.Handler {
	if m == nil {
		return next
	}

	return promhttp.InstrumentHandlerDuration(
		m.reqTime.MustCurryWith(prometheus.Labels{"handler": handler}),
		promhttp.InstrumentHandlerCounter(m.reqStatus, next),
	)
}
