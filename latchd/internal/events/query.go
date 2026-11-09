package events

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kayaramazan/latch/proto/model"
)

// Filter selects events for Query.
type Filter struct {
	Since      time.Time
	Until      time.Time
	RouteID    string
	ClientIP   string
	Status     int
	Decision   string
	DenyReason string
	Text       string // substring match on path or hostname
	Limit      int
}

// Query returns events matching the filter, most recent first.
func (s *Store) Query(f Filter) ([]model.AccessEvent, error) {
	var where []string
	var args []any
	if !f.Since.IsZero() {
		where = append(where, "ts_unix >= ?")
		args = append(args, f.Since.Unix())
	}
	if !f.Until.IsZero() {
		where = append(where, "ts_unix <= ?")
		args = append(args, f.Until.Unix())
	}
	if f.RouteID != "" {
		where = append(where, "route_id = ?")
		args = append(args, f.RouteID)
	}
	if f.ClientIP != "" {
		where = append(where, "client_ip = ?")
		args = append(args, f.ClientIP)
	}
	if f.Status != 0 {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Decision != "" {
		where = append(where, "decision = ?")
		args = append(args, f.Decision)
	}
	if f.DenyReason != "" {
		where = append(where, "deny_reason = ?")
		args = append(args, f.DenyReason)
	}
	if f.Text != "" {
		where = append(where, "(path LIKE ? OR hostname LIKE ?)")
		like := "%" + f.Text + "%"
		args = append(args, like, like)
	}
	q := "SELECT ts, source, relay_id, route_id, hostname, client_ip, country, method, path, status, duration_ms, bytes_in, bytes_out, target, decision, deny_reason, rule_id, where_loc FROM events"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 || limit > 10000 {
		limit = 200
	}
	q += fmt.Sprintf(" ORDER BY ts_unix DESC, id DESC LIMIT %d", limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AccessEvent
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEvent(r rowScanner) (model.AccessEvent, error) {
	var ev model.AccessEvent
	var ts string
	if err := r.Scan(&ts, &ev.Source, &ev.RelayID, &ev.RouteID, &ev.Hostname, &ev.ClientIP, &ev.Country,
		&ev.Method, &ev.Path, &ev.Status, &ev.DurationMs, &ev.BytesIn, &ev.BytesOut, &ev.Target,
		&ev.Decision, &ev.DenyReason, &ev.RuleID, &ev.Where); err != nil {
		return ev, err
	}
	ev.TS, _ = time.Parse(time.RFC3339Nano, ts)
	return ev, nil
}

// RouteStat is per-route traffic.
type RouteStat struct {
	RouteID  string `json:"routeId"`
	Hostname string `json:"hostname"`
	Count    int    `json:"count"`
	Denied   int    `json:"denied"`
}

// IPStat is per-IP denial count.
type IPStat struct {
	ClientIP string `json:"clientIp"`
	Count    int    `json:"count"`
}

// Dashboard is the server-side aggregation over a window.
type Dashboard struct {
	WindowSec        int                 `json:"windowSec"`
	Requests         int                 `json:"requests"`
	RequestsPerSec   float64             `json:"requestsPerSec"`
	Denied           int                 `json:"denied"`
	P50Ms            float64             `json:"p50Ms"`
	P95Ms            float64             `json:"p95Ms"`
	P99Ms            float64             `json:"p99Ms"`
	StatusClasses    map[string]int      `json:"statusClasses"`
	TopRoutes        []RouteStat         `json:"topRoutes"`
	CurrentlyBlocked []model.AccessEvent `json:"currentlyBlocked"`
	TopBlockedIPs    []IPStat            `json:"topBlockedIpsLastHour"`
}

// Aggregate computes the dashboard over the last windowSec seconds.
func (s *Store) Aggregate(windowSec int) (Dashboard, error) {
	if windowSec <= 0 {
		windowSec = 300
	}
	since := time.Now().Add(-time.Duration(windowSec) * time.Second).Unix()
	d := Dashboard{WindowSec: windowSec, StatusClasses: map[string]int{}}

	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(decision='deny'),0) FROM events WHERE ts_unix >= ?`, since).
		Scan(&d.Requests, &d.Denied); err != nil {
		return d, err
	}
	d.RequestsPerSec = float64(d.Requests) / float64(windowSec)

	// Status classes.
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM events WHERE ts_unix >= ? GROUP BY status`, since)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var status, n int
		_ = rows.Scan(&status, &n)
		d.StatusClasses[statusClass(status)] += n
	}
	rows.Close()

	// Latency percentiles (sampled, allow-with-status only).
	durRows, err := s.db.Query(`SELECT duration_ms FROM events WHERE ts_unix >= ? AND status > 0 ORDER BY duration_ms LIMIT 100000`, since)
	if err != nil {
		return d, err
	}
	var durs []float64
	for durRows.Next() {
		var ms int64
		_ = durRows.Scan(&ms)
		durs = append(durs, float64(ms))
	}
	durRows.Close()
	d.P50Ms, d.P95Ms, d.P99Ms = percentile(durs, 50), percentile(durs, 95), percentile(durs, 99)

	// Top routes.
	rRows, err := s.db.Query(`SELECT route_id, COALESCE(MAX(hostname),''), COUNT(*), COALESCE(SUM(decision='deny'),0)
		FROM events WHERE ts_unix >= ? AND route_id != '' GROUP BY route_id ORDER BY COUNT(*) DESC LIMIT 10`, since)
	if err != nil {
		return d, err
	}
	for rRows.Next() {
		var rs RouteStat
		_ = rRows.Scan(&rs.RouteID, &rs.Hostname, &rs.Count, &rs.Denied)
		d.TopRoutes = append(d.TopRoutes, rs)
	}
	rRows.Close()

	// Currently blocked: recent denies.
	blocked, err := s.Query(Filter{Decision: string(model.DecisionDeny), Limit: 20})
	if err != nil {
		return d, err
	}
	d.CurrentlyBlocked = blocked

	// Top blocked IPs in the last hour.
	hourAgo := time.Now().Add(-time.Hour).Unix()
	ipRows, err := s.db.Query(`SELECT client_ip, COUNT(*) FROM events WHERE decision='deny' AND ts_unix >= ? AND client_ip != ''
		GROUP BY client_ip ORDER BY COUNT(*) DESC LIMIT 10`, hourAgo)
	if err != nil {
		return d, err
	}
	for ipRows.Next() {
		var is IPStat
		_ = ipRows.Scan(&is.ClientIP, &is.Count)
		d.TopBlockedIPs = append(d.TopBlockedIPs, is)
	}
	ipRows.Close()

	return d, nil
}

// percentile returns the p-th percentile (0-100) of a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if !sort.Float64sAreSorted(sorted) {
		sort.Float64s(sorted)
	}
	idx := int(p / 100 * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
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
