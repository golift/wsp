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
	Pools     prometheus.Gauge
	Conns     prometheus.Gauge
	Busy      prometheus.Gauge
	Idle      prometheus.Gauge
	Closed    prometheus.Gauge
	Regs      prometheus.Counter
	RegFail   prometheus.Counter
	Uptime    prometheus.CounterFunc
	PoolConns *prometheus.GaugeVec
	reqStatus *prometheus.CounterVec
	reqTime   *prometheus.HistogramVec
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
		Uptime: promauto.NewCounterFunc(prometheus.CounterOpts{
			Name: "mulery_uptime_seconds_total",
			Help: "Seconds Mulery has been running",
		}, func() float64 { return time.Since(start).Seconds() }),
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
