// Package metrics is latchd's Prometheus instrumentation, exposed on the
// management API's /metrics. It covers both local and relay traffic.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/kayaramazan/latch/proto/model"
)

// Metrics holds latchd's collectors.
type Metrics struct {
	reg        *prometheus.Registry
	requests   *prometheus.CounterVec
	denied     *prometheus.CounterVec
	byRoute    *prometheus.CounterVec
	latency    *prometheus.HistogramVec
	bytesIn    prometheus.Counter
	bytesOut   prometheus.Counter
	tunnels    prometheus.Gauge
	reconnects *prometheus.CounterVec
	health     *prometheus.GaugeVec
}

// New builds a Metrics with its own registry.
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "latchd_requests_total", Help: "Requests by source, decision and status class."},
			[]string{"source", "decision", "status"}),
		denied: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "latchd_denied_total", Help: "Denied requests by reason."}, []string{"reason"}),
		byRoute: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "latchd_route_requests_total", Help: "Requests per route."}, []string{"route"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "latchd_request_duration_seconds", Help: "Request duration.", Buckets: prometheus.DefBuckets},
			[]string{"source"}),
		bytesIn:  prometheus.NewCounter(prometheus.CounterOpts{Name: "latchd_bytes_in_total", Help: "Bytes in."}),
		bytesOut: prometheus.NewCounter(prometheus.CounterOpts{Name: "latchd_bytes_out_total", Help: "Bytes out."}),
		tunnels:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "latchd_tunnels_connected", Help: "Connected relays."}),
		reconnects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "latchd_reconnects_total", Help: "Relay reconnects."}, []string{"relay"}),
		health: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "latchd_upstream_healthy", Help: "1 healthy, 0 unhealthy, -1 unknown."}, []string{"route"}),
	}
	m.reg.MustRegister(m.requests, m.denied, m.byRoute, m.latency, m.bytesIn, m.bytesOut, m.tunnels, m.reconnects, m.health)
	return m
}

// Append records one event (implements tunnel.EventSink).
func (m *Metrics) Append(ev model.AccessEvent) {
	source := string(ev.Source)
	if source == "" {
		source = "unknown"
	}
	m.requests.WithLabelValues(source, string(ev.Decision), statusClass(ev.Status)).Inc()
	if ev.Decision == model.DecisionDeny && ev.DenyReason != "" {
		m.denied.WithLabelValues(ev.DenyReason).Inc()
	}
	if ev.RouteID != "" {
		m.byRoute.WithLabelValues(ev.RouteID).Inc()
	}
	m.latency.WithLabelValues(source).Observe(float64(ev.DurationMs) / 1000)
	if ev.BytesIn > 0 {
		m.bytesIn.Add(float64(ev.BytesIn))
	}
	if ev.BytesOut > 0 {
		m.bytesOut.Add(float64(ev.BytesOut))
	}
}

// SetTunnels sets the connected-relay gauge.
func (m *Metrics) SetTunnels(n int) { m.tunnels.Set(float64(n)) }

// SetHealth updates per-route upstream health gauges.
func (m *Metrics) SetHealth(status map[string]string) {
	for route, st := range status {
		var v float64
		switch st {
		case "healthy":
			v = 1
		case "unhealthy":
			v = 0
		default:
			v = -1
		}
		m.health.WithLabelValues(route).Set(v)
	}
}

// Handler serves the Prometheus exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

func statusClass(status int) string {
	switch {
	case status == 0:
		return "none"
	case status < 200:
		return "1xx"
	case status < 300:
		return "2xx"
	case status < 400:
		return "3xx"
	case status < 500:
		return "4xx"
	default:
		return "5xx"
	}
}
