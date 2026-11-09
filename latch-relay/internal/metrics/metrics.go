// Package metrics is the relay's Prometheus instrumentation: request counts,
// latency, denial reasons and live agent/tunnel gauges, exposed on /metrics.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/kayaramazan/latch/proto/model"
)

// Metrics holds the relay's collectors.
type Metrics struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	denied   *prometheus.CounterVec
	latency  *prometheus.HistogramVec
	bytesIn  prometheus.Counter
	bytesOut prometheus.Counter
	agents   prometheus.Gauge
	tunnels  prometheus.Gauge
}

// New builds a Metrics with its own registry.
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "latch_relay_requests_total", Help: "HTTP requests by decision and status class.",
		}, []string{"decision", "status"}),
		denied: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "latch_relay_denied_total", Help: "Denied requests by reason.",
		}, []string{"reason"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "latch_relay_request_duration_seconds", Help: "Request duration.",
			Buckets: prometheus.DefBuckets,
		}, []string{"decision"}),
		bytesIn: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "latch_relay_bytes_in_total", Help: "Bytes received from clients.",
		}),
		bytesOut: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "latch_relay_bytes_out_total", Help: "Bytes sent to clients.",
		}),
		agents: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "latch_relay_agents", Help: "Connected agents.",
		}),
		tunnels: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "latch_relay_tunnels", Help: "Registered routes (online + offline).",
		}),
	}
	m.reg.MustRegister(m.requests, m.denied, m.latency, m.bytesIn, m.bytesOut, m.agents, m.tunnels)
	return m
}

// Record updates counters for one access event.
func (m *Metrics) Record(ev model.AccessEvent) {
	decision := string(ev.Decision)
	m.requests.WithLabelValues(decision, statusClass(ev.Status)).Inc()
	if ev.Decision == model.DecisionDeny && ev.DenyReason != "" {
		m.denied.WithLabelValues(ev.DenyReason).Inc()
	}
	m.latency.WithLabelValues(decision).Observe(float64(ev.DurationMs) / 1000)
	if ev.BytesIn > 0 {
		m.bytesIn.Add(float64(ev.BytesIn))
	}
	if ev.BytesOut > 0 {
		m.bytesOut.Add(float64(ev.BytesOut))
	}
}

// SetAgents and SetTunnels update the live gauges.
func (m *Metrics) SetAgents(n int)  { m.agents.Set(float64(n)) }
func (m *Metrics) SetTunnels(n int) { m.tunnels.Set(float64(n)) }

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
