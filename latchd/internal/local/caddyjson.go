// Package local runs the agent's local reverse-proxy mode: it turns the
// model's local routes into a Caddy JSON config, embeds Caddy as a library,
// and enforces the same /policy engine as the relay via a custom handler.
package local

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/kayaramazan/latch/proto/model"
)

// IssuerMode selects how certificates are obtained for auto-TLS routes.
type IssuerMode string

const (
	// IssuerACME uses Let's Encrypt (production); the default for real servers.
	IssuerACME IssuerMode = "acme"
	// IssuerInternal uses Caddy's local CA; used in tests.
	IssuerInternal IssuerMode = "internal"
)

// Options parameterize config generation.
type Options struct {
	HTTPPort   int
	HTTPSPort  int
	StorageDir string     // Caddy data dir (certs, ACME state)
	Issuer     IssuerMode // acme | internal
	ACMEEmail  string
	ACMECA     string // optional ACME directory override (staging/Pebble)
	HTTP3      bool   // enable HTTP/3 (QUIC) on the HTTPS server
}

// Generate renders the Caddy JSON config for the snapshot's local routes. It
// returns an empty-but-valid config (no servers) when there are no local
// routes. tls:off routes are skipped (see NOTES; deferred).
func Generate(snap *model.Snapshot, opts Options) ([]byte, error) {
	if opts.HTTPPort == 0 {
		opts.HTTPPort = 80
	}
	if opts.HTTPSPort == 0 {
		opts.HTTPSPort = 443
	}
	if opts.Issuer == "" {
		opts.Issuer = IssuerACME
	}

	var routes []any
	var autoSubjects []string
	var customCerts []any
	seen := map[string]bool{}

	locals := localRoutes(snap)
	for _, rt := range locals {
		if rt.TLS == model.TLSOff {
			continue // deferred: plain-HTTP local routes
		}
		routes = append(routes, caddyRoute(rt))
		for _, h := range rt.Hostnames {
			if seen[h] {
				continue
			}
			seen[h] = true
			switch rt.TLS {
			case model.TLSCustom:
				// handled by load_files below
			default:
				autoSubjects = append(autoSubjects, h)
			}
		}
		if rt.TLS == model.TLSCustom && rt.TLSCertFile != "" {
			customCerts = append(customCerts, map[string]any{
				"certificate": rt.TLSCertFile, "key": rt.TLSKeyFile,
			})
		}
	}

	httpApp := map[string]any{
		"http_port":  opts.HTTPPort,
		"https_port": opts.HTTPSPort,
	}
	if len(routes) > 0 {
		protocols := []string{"h1", "h2"}
		if opts.HTTP3 {
			protocols = append(protocols, "h3")
		}
		httpApp["servers"] = map[string]any{
			"latch": map[string]any{
				"listen":    []string{fmt.Sprintf(":%d", opts.HTTPSPort)},
				"routes":    routes,
				"protocols": protocols,
			},
		}
	}

	apps := map[string]any{"http": httpApp}
	if tlsApp := tlsApp(autoSubjects, customCerts, opts); tlsApp != nil {
		apps["tls"] = tlsApp
	}
	if opts.Issuer == IssuerInternal && len(autoSubjects) > 0 {
		// Do not touch the system trust store from the agent.
		apps["pki"] = map[string]any{"certificate_authorities": map[string]any{
			"local": map[string]any{"install_trust": false}}}
	}

	cfg := map[string]any{
		"admin":   map[string]any{"disabled": true},
		"logging": map[string]any{"logs": map[string]any{"default": map[string]any{"level": "ERROR"}}},
		"apps":    apps,
	}
	if opts.StorageDir != "" {
		cfg["storage"] = map[string]any{"module": "file_system", "root": opts.StorageDir}
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// caddyRoute builds one Caddy route for a local model route.
func caddyRoute(rt model.Route) map[string]any {
	match := map[string]any{"host": rt.Hostnames}
	if rt.PathPrefix != "" && rt.PathPrefix != "/" {
		match["path"] = []string{rt.PathPrefix + "*"}
	}
	inner := []any{
		map[string]any{"handler": "latch_policy", "route_id": rt.ID},
		map[string]any{
			"handler":   "reverse_proxy",
			"upstreams": []any{map[string]any{"dial": rt.Target}},
		},
	}
	if hc := activeHealthCheck(rt.HealthCheck); hc != nil {
		rp := inner[1].(map[string]any)
		rp["health_checks"] = map[string]any{"active": hc}
	}
	return map[string]any{
		"match":    []any{match},
		"handle":   []any{map[string]any{"handler": "subroute", "routes": []any{map[string]any{"handle": inner}}}},
		"terminal": true,
	}
}

func activeHealthCheck(hc model.HealthCheck) map[string]any {
	if !hc.Enabled {
		return nil
	}
	out := map[string]any{}
	if hc.Path != "" {
		out["path"] = hc.Path
	}
	if hc.IntervalSec > 0 {
		out["interval"] = fmt.Sprintf("%ds", hc.IntervalSec)
	}
	if hc.TimeoutSec > 0 {
		out["timeout"] = fmt.Sprintf("%ds", hc.TimeoutSec)
	}
	if len(hc.ExpectStatus) > 0 {
		out["expect_status"] = hc.ExpectStatus[0]
	}
	return out
}

// tlsApp builds the tls app (automation + loaded custom certs), or nil.
func tlsApp(autoSubjects []string, customCerts []any, opts Options) map[string]any {
	app := map[string]any{}
	if len(autoSubjects) > 0 {
		sort.Strings(autoSubjects)
		issuer := map[string]any{}
		switch opts.Issuer {
		case IssuerInternal:
			issuer = map[string]any{"module": "internal"}
		default:
			issuer = map[string]any{"module": "acme"}
			if opts.ACMEEmail != "" {
				issuer["email"] = opts.ACMEEmail
			}
			if opts.ACMECA != "" {
				issuer["ca"] = opts.ACMECA
			}
		}
		app["automation"] = map[string]any{
			"policies": []any{map[string]any{"subjects": autoSubjects, "issuers": []any{issuer}}},
		}
	}
	if len(customCerts) > 0 {
		app["certificates"] = map[string]any{"load_files": customCerts}
	}
	if len(app) == 0 {
		return nil
	}
	return app
}

// localRoutes returns enabled local routes, sorted by ID for stable output.
func localRoutes(snap *model.Snapshot) []model.Route {
	var out []model.Route
	for _, rt := range snap.Routes {
		if rt.Exposure == model.ExposureLocal && rt.Enabled {
			out = append(out, rt)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
