package model

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FieldError describes one invalid field. Path uses the JSON names and
// indexes, e.g. "routes[2].hostnames[0]".
type FieldError struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e FieldError) Error() string { return e.Path + ": " + e.Message }

// ValidationError is the error returned by Validate; it carries every
// FieldError found, sorted by path.
type ValidationError struct {
	Fields []FieldError `json:"fields"`
}

func (e *ValidationError) Error() string {
	if len(e.Fields) == 1 {
		return "validation failed: " + e.Fields[0].Error()
	}
	return fmt.Sprintf("validation failed: %d errors (first: %s)", len(e.Fields), e.Fields[0].Error())
}

// Error codes used in FieldError.Code.
const (
	CodeRequired     = "required"
	CodeInvalid      = "invalid"
	CodeTooLong      = "too_long"
	CodeTooMany      = "too_many"
	CodeOutOfRange   = "out_of_range"
	CodeDuplicate    = "duplicate"
	CodeUnknownRef   = "unknown_reference"
	CodeConflict     = "conflict"
	CodeNotAllowed   = "not_allowed"
	CodeInvalidHost  = "invalid_hostname"
	CodeInvalidPort  = "invalid_port"
	CodeInvalidCIDR  = "invalid_cidr"
	CodeInsecureBind = "insecure_bind"
)

// Limits from docs/DATA_MODEL.md.
const (
	MaxNameLen        = 64
	MaxHeaderRules    = 32
	MaxIPRules        = 2000
	MaxRateLimitRefs  = 8
	MaxRateLimitKeys  = 3
	MaxWindowSec      = 3600
	MaxHostnameLen    = 253
	MaxLabelLen       = 63
	MinPort, MaxPort  = 1, 65535
	MinDelayMs        = 0
	MaxDelayMs        = 60000
	MaxTrustedProxies = 256
)

type validator struct {
	errs []FieldError
}

func (v *validator) add(path, code, format string, args ...any) {
	v.errs = append(v.errs, FieldError{Path: path, Code: code, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) result() error {
	if len(v.errs) == 0 {
		return nil
	}
	sort.SliceStable(v.errs, func(i, j int) bool { return v.errs[i].Path < v.errs[j].Path })
	return &ValidationError{Fields: v.errs}
}

// Validate checks the whole snapshot, including cross-object rules
// (references, hostname collisions, settings consistency). Callers should
// Normalize first. It returns nil or a *ValidationError.
func Validate(s *Snapshot) error {
	v := &validator{}
	v.settings(&s.Settings)

	policyIDs := map[string]int{}
	for i := range s.IPPolicies {
		v.ipPolicy(fmt.Sprintf("ipPolicies[%d]", i), &s.IPPolicies[i], policyIDs)
	}
	rateIDs := map[string]int{}
	for i := range s.RateLimits {
		v.rateLimit(fmt.Sprintf("rateLimits[%d]", i), &s.RateLimits[i], rateIDs)
	}
	relayIDs := map[string]int{}
	for i := range s.Relays {
		v.relay(fmt.Sprintf("relays[%d]", i), &s.Relays[i], relayIDs)
	}
	routeIDs := map[string]int{}
	routeNames := map[string]int{}
	type hostKey struct{ host, prefix string }
	hostOwners := map[hostKey]int{}
	for i := range s.Routes {
		r := &s.Routes[i]
		p := fmt.Sprintf("routes[%d]", i)
		v.route(p, r, s, policyIDs, rateIDs, relayIDs)
		if j, dup := routeIDs[r.ID]; dup {
			v.add(p+".id", CodeDuplicate, "same id as routes[%d]", j)
		} else {
			routeIDs[r.ID] = i
		}
		if r.Name != "" {
			if j, dup := routeNames[strings.ToLower(r.Name)]; dup {
				v.add(p+".name", CodeDuplicate, "same name as routes[%d]", j)
			} else {
				routeNames[strings.ToLower(r.Name)] = i
			}
		}
		for hi, h := range r.Hostnames {
			k := hostKey{h, r.PathPrefix}
			if j, dup := hostOwners[k]; dup {
				v.add(fmt.Sprintf("%s.hostnames[%d]", p, hi), CodeConflict,
					"hostname %q with path prefix %q is already used by routes[%d]", h, r.PathPrefix, j)
			} else {
				hostOwners[k] = i
			}
		}
	}
	return v.result()
}

func (v *validator) route(p string, r *Route, s *Snapshot, policies, rates, relays map[string]int) {
	if !ValidID(r.ID, PrefixRoute) {
		v.add(p+".id", CodeInvalid, "malformed route id")
	}
	v.name(p+".name", r.Name)
	if r.Target == "" {
		v.add(p+".target", CodeRequired, "target is required")
	} else if err := validateHostPort(r.Target); err != nil {
		v.add(p+".target", CodeInvalid, "target must be host:port: %v", err)
	}
	switch r.Exposure {
	case ExposureLocal, ExposureTunnel:
	case "":
		v.add(p+".exposure", CodeRequired, "exposure is required")
	default:
		v.add(p+".exposure", CodeInvalid, "exposure must be local or tunnel")
	}
	switch r.Kind {
	case KindHTTP, KindTCP:
	default:
		v.add(p+".kind", CodeInvalid, "kind must be http or tcp")
	}
	if r.Exposure == ExposureTunnel {
		if r.RelayID == "" {
			v.add(p+".relayId", CodeRequired, "tunnel routes need a relay")
		} else if _, ok := relays[r.RelayID]; !ok {
			v.add(p+".relayId", CodeUnknownRef, "relay %q does not exist", r.RelayID)
		}
		if r.TLS != TLSAuto {
			v.add(p+".tls", CodeNotAllowed, "tunnel routes always use the relay's certificate (tls must be auto)")
		}
	} else if r.RelayID != "" {
		v.add(p+".relayId", CodeNotAllowed, "local routes cannot reference a relay")
	}
	if r.Kind == KindTCP {
		if r.Exposure != ExposureTunnel {
			v.add(p+".kind", CodeNotAllowed, "tcp routes are tunnel-only in v1")
		}
		if len(r.Hostnames) != 0 {
			v.add(p+".hostnames", CodeNotAllowed, "tcp routes have no hostnames")
		}
		if r.TCPPort != 0 && (r.TCPPort < MinPort || r.TCPPort > MaxPort) {
			v.add(p+".tcpPort", CodeInvalidPort, "port must be 1-65535 or 0 for automatic")
		}
	} else {
		if len(r.Hostnames) == 0 {
			v.add(p+".hostnames", CodeRequired, "at least one hostname is required")
		}
		seen := map[string]bool{}
		for i, h := range r.Hostnames {
			hp := fmt.Sprintf("%s.hostnames[%d]", p, i)
			if err := ValidateHostname(h); err != nil {
				v.add(hp, CodeInvalidHost, "%v", err)
			}
			if seen[h] {
				v.add(hp, CodeDuplicate, "hostname repeated")
			}
			seen[h] = true
		}
		if r.TCPPort != 0 {
			v.add(p+".tcpPort", CodeNotAllowed, "tcpPort only applies to tcp routes")
		}
		if r.ProxyProtocol {
			v.add(p+".proxyProtocol", CodeNotAllowed, "proxyProtocol only applies to tcp routes")
		}
	}
	if !strings.HasPrefix(r.PathPrefix, "/") {
		v.add(p+".pathPrefix", CodeInvalid, "must start with /")
	} else if strings.ContainsAny(r.PathPrefix, " ?#") {
		v.add(p+".pathPrefix", CodeInvalid, "must not contain spaces, ? or #")
	}
	switch r.TLS {
	case TLSAuto:
	case TLSCustom:
		if r.TLSCertFile == "" {
			v.add(p+".tlsCertFile", CodeRequired, "required when tls is custom")
		}
		if r.TLSKeyFile == "" {
			v.add(p+".tlsKeyFile", CodeRequired, "required when tls is custom")
		}
	case TLSOff:
		if r.HTTPSRedirect {
			v.add(p+".httpsRedirect", CodeNotAllowed, "cannot redirect to https when tls is off")
		}
	default:
		v.add(p+".tls", CodeInvalid, "tls must be auto, custom or off")
	}
	if r.TLS != TLSCustom && (r.TLSCertFile != "" || r.TLSKeyFile != "") {
		v.add(p+".tlsCertFile", CodeNotAllowed, "certificate files only apply when tls is custom")
	}
	if len(r.Headers) > MaxHeaderRules {
		v.add(p+".headers", CodeTooMany, "at most %d header rules", MaxHeaderRules)
	}
	for i := range r.Headers {
		v.headerRule(fmt.Sprintf("%s.headers[%d]", p, i), &r.Headers[i])
	}
	v.healthCheck(p+".healthCheck", &r.HealthCheck, r.Kind)
	if r.IPPolicyID != "" {
		if _, ok := policies[r.IPPolicyID]; !ok {
			v.add(p+".ipPolicyId", CodeUnknownRef, "ip policy %q does not exist", r.IPPolicyID)
		}
	}
	if len(r.RateLimitIDs) > MaxRateLimitRefs {
		v.add(p+".rateLimitIds", CodeTooMany, "at most %d rate limits per route", MaxRateLimitRefs)
	}
	seenRL := map[string]bool{}
	for i, id := range r.RateLimitIDs {
		rp := fmt.Sprintf("%s.rateLimitIds[%d]", p, i)
		if _, ok := rates[id]; !ok {
			v.add(rp, CodeUnknownRef, "rate limit %q does not exist", id)
		}
		if seenRL[id] {
			v.add(rp, CodeDuplicate, "rate limit repeated")
		}
		seenRL[id] = true
	}
}

var headerNameRe = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_` + "`" + `|~-]+$`)

var hopByHop = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-authenticate": true, "proxy-authorization": true,
	"te": true, "trailer": true, "transfer-encoding": true, "upgrade": true, "host": true, "content-length": true,
}

func (v *validator) headerRule(p string, h *HeaderRule) {
	switch h.Phase {
	case HeaderRequest, HeaderResponse:
	default:
		v.add(p+".phase", CodeInvalid, "phase must be request or response")
	}
	switch h.Op {
	case HeaderSet, HeaderAdd:
		if h.Value == "" {
			v.add(p+".value", CodeRequired, "value is required for %s", h.Op)
		}
	case HeaderDelete:
	default:
		v.add(p+".op", CodeInvalid, "op must be set, add or delete")
	}
	if h.Name == "" {
		v.add(p+".name", CodeRequired, "header name is required")
	} else if !headerNameRe.MatchString(h.Name) {
		v.add(p+".name", CodeInvalid, "not a valid header name")
	} else if hopByHop[strings.ToLower(h.Name)] {
		v.add(p+".name", CodeNotAllowed, "%s is managed by the proxy", h.Name)
	}
	if strings.ContainsAny(h.Value, "\r\n") {
		v.add(p+".value", CodeInvalid, "value must not contain line breaks")
	}
}

func (v *validator) healthCheck(p string, h *HealthCheck, kind RouteKind) {
	if h.IntervalSec < 1 || h.IntervalSec > 3600 {
		v.add(p+".intervalSec", CodeOutOfRange, "must be 1-3600")
	}
	if h.TimeoutSec < 1 || h.TimeoutSec > 60 {
		v.add(p+".timeoutSec", CodeOutOfRange, "must be 1-60")
	}
	if h.TimeoutSec >= h.IntervalSec && h.IntervalSec >= 1 {
		v.add(p+".timeoutSec", CodeOutOfRange, "must be smaller than intervalSec")
	}
	if h.UnhealthyAfter < 1 || h.UnhealthyAfter > 100 {
		v.add(p+".unhealthyAfter", CodeOutOfRange, "must be 1-100")
	}
	if h.HealthyAfter < 1 || h.HealthyAfter > 100 {
		v.add(p+".healthyAfter", CodeOutOfRange, "must be 1-100")
	}
	if kind == KindHTTP {
		if !strings.HasPrefix(h.Path, "/") {
			v.add(p+".path", CodeInvalid, "must start with /")
		}
		if len(h.ExpectStatus) != 2 || h.ExpectStatus[0] < 100 || h.ExpectStatus[1] > 599 || h.ExpectStatus[0] > h.ExpectStatus[1] {
			v.add(p+".expectStatus", CodeInvalid, "must be [min, max] within 100-599")
		}
	}
}

func (v *validator) ipPolicy(p string, pol *IPPolicy, ids map[string]int) {
	if !ValidID(pol.ID, PrefixIPPolicy) {
		v.add(p+".id", CodeInvalid, "malformed policy id")
	}
	if j, dup := ids[pol.ID]; dup {
		v.add(p+".id", CodeDuplicate, "same id as ipPolicies[%d]", j)
	} else {
		ids[pol.ID] = len(ids)
	}
	v.name(p+".name", pol.Name)
	switch pol.DefaultAction {
	case ActionAllow, ActionDeny:
	default:
		v.add(p+".defaultAction", CodeInvalid, "must be allow or deny")
	}
	switch pol.OnDeny.Mode {
	case DenyStatus:
		if pol.OnDeny.Status != 403 && pol.OnDeny.Status != 404 {
			v.add(p+".onDeny.status", CodeInvalid, "must be 403 or 404")
		}
	case DenyDrop:
		if pol.OnDeny.Status != 0 {
			v.add(p+".onDeny.status", CodeNotAllowed, "status does not apply to drop")
		}
	default:
		v.add(p+".onDeny.mode", CodeInvalid, "must be status or drop")
	}
	if len(pol.Rules) > MaxIPRules {
		v.add(p+".rules", CodeTooMany, "at most %d rules", MaxIPRules)
	}
	for i, r := range pol.Rules {
		rp := fmt.Sprintf("%s.rules[%d]", p, i)
		switch r.Action {
		case ActionAllow, ActionDeny:
		default:
			v.add(rp+".action", CodeInvalid, "must be allow or deny")
		}
		switch {
		case r.CIDR != "" && r.Country != "":
			v.add(rp, CodeInvalid, "set either cidr or country, not both")
		case r.CIDR != "":
			if _, err := netip.ParsePrefix(r.CIDR); err != nil {
				v.add(rp+".cidr", CodeInvalidCIDR, "%v", err)
			}
		case r.Country != "":
			if len(r.Country) != 2 || strings.ToUpper(r.Country) != r.Country || !isAlpha(r.Country) {
				v.add(rp+".country", CodeInvalid, "must be an ISO 3166-1 alpha-2 code like TR")
			}
		default:
			v.add(rp, CodeRequired, "rule needs a cidr or a country")
		}
	}
}

func (v *validator) rateLimit(p string, r *RateLimitRule, ids map[string]int) {
	if !ValidID(r.ID, PrefixRateLimit) {
		v.add(p+".id", CodeInvalid, "malformed rate limit id")
	}
	if j, dup := ids[r.ID]; dup {
		v.add(p+".id", CodeDuplicate, "same id as rateLimits[%d]", j)
	} else {
		ids[r.ID] = len(ids)
	}
	v.name(p+".name", r.Name)
	if len(r.Keys) == 0 || len(r.Keys) > MaxRateLimitKeys {
		v.add(p+".keys", CodeOutOfRange, "1-%d keys", MaxRateLimitKeys)
	}
	seen := map[string]bool{}
	for i, k := range r.Keys {
		kp := fmt.Sprintf("%s.keys[%d]", p, i)
		switch {
		case k == "ip" || k == "path":
		case strings.HasPrefix(k, "header:") && headerNameRe.MatchString(strings.TrimPrefix(k, "header:")):
		default:
			v.add(kp, CodeInvalid, "must be ip, path or header:<Name>")
		}
		if seen[k] {
			v.add(kp, CodeDuplicate, "key repeated")
		}
		seen[k] = true
	}
	if r.Limit < 1 {
		v.add(p+".limit", CodeOutOfRange, "must be at least 1")
	}
	if r.WindowSec < 1 || r.WindowSec > MaxWindowSec {
		v.add(p+".windowSec", CodeOutOfRange, "must be 1-%d", MaxWindowSec)
	}
	switch r.OnExceed.Mode {
	case ExceedReject:
		if r.OnExceed.MaxDelayMs != 0 {
			v.add(p+".onExceed.maxDelayMs", CodeNotAllowed, "only applies to delay")
		}
	case ExceedDelay:
		if r.OnExceed.MaxDelayMs < 1 || r.OnExceed.MaxDelayMs > MaxDelayMs {
			v.add(p+".onExceed.maxDelayMs", CodeOutOfRange, "must be 1-%d", MaxDelayMs)
		}
	default:
		v.add(p+".onExceed.mode", CodeInvalid, "must be reject or delay")
	}
}

func (v *validator) relay(p string, r *Relay, ids map[string]int) {
	if !ValidID(r.ID, PrefixRelay) {
		v.add(p+".id", CodeInvalid, "malformed relay id")
	}
	if j, dup := ids[r.ID]; dup {
		v.add(p+".id", CodeDuplicate, "same id as relays[%d]", j)
	} else {
		ids[r.ID] = len(ids)
	}
	v.name(p+".name", r.Name)
	if r.Host == "" {
		v.add(p+".host", CodeRequired, "host is required")
	} else if err := ValidateHostname(r.Host); err != nil {
		if _, ipErr := netip.ParseAddr(r.Host); ipErr != nil {
			v.add(p+".host", CodeInvalidHost, "%v", err)
		}
	}
	if r.Port < MinPort || r.Port > MaxPort {
		v.add(p+".port", CodeInvalidPort, "must be 1-65535")
	}
	if r.Token == "" {
		v.add(p+".token", CodeRequired, "token is required")
	}
	if r.CAPEM != "" && !strings.Contains(r.CAPEM, "-----BEGIN CERTIFICATE-----") {
		v.add(p+".caPem", CodeInvalid, "must be a PEM certificate")
	}
	if r.BaseDomain != "" {
		if err := ValidateHostname(r.BaseDomain); err != nil {
			v.add(p+".baseDomain", CodeInvalidHost, "%v", err)
		}
	}
}

func (v *validator) settings(s *Settings) {
	const p = "settings"
	host, port, err := net.SplitHostPort(s.Listen)
	if err != nil {
		v.add(p+".listen", CodeInvalid, "must be host:port")
	} else {
		if n, err := strconv.Atoi(port); err != nil || n < MinPort || n > MaxPort {
			v.add(p+".listen", CodeInvalidPort, "port must be 1-65535")
		}
		if ip, err := netip.ParseAddr(host); err != nil {
			if host != "localhost" {
				v.add(p+".listen", CodeInvalid, "host must be an IP address or localhost")
			}
		} else if !ip.IsLoopback() && s.Auth.Mode == AuthNone {
			v.add(p+".auth.mode", CodeInsecureBind, "a password is required when listening on %s", host)
		}
	}
	switch s.Auth.Mode {
	case AuthNone:
		if s.Auth.PasswordHash != "" {
			v.add(p+".auth.passwordHash", CodeNotAllowed, "does not apply when auth mode is none")
		}
	case AuthPassword:
		if s.Auth.PasswordHash == "" {
			v.add(p+".auth.passwordHash", CodeRequired, "set a password")
		}
	default:
		v.add(p+".auth.mode", CodeInvalid, "must be none or password")
	}
	if len(s.TrustedProxies) > MaxTrustedProxies {
		v.add(p+".trustedProxies", CodeTooMany, "at most %d entries", MaxTrustedProxies)
	}
	for i, c := range s.TrustedProxies {
		if _, err := netip.ParsePrefix(c); err != nil {
			if _, err2 := netip.ParseAddr(c); err2 != nil {
				v.add(fmt.Sprintf("%s.trustedProxies[%d]", p, i), CodeInvalidCIDR, "must be an IP or CIDR")
			}
		}
	}
	if s.LocalMode.HTTPPort < MinPort || s.LocalMode.HTTPPort > MaxPort {
		v.add(p+".localMode.httpPort", CodeInvalidPort, "must be 1-65535")
	}
	if s.LocalMode.HTTPSPort < MinPort || s.LocalMode.HTTPSPort > MaxPort {
		v.add(p+".localMode.httpsPort", CodeInvalidPort, "must be 1-65535")
	}
	if s.LocalMode.HTTPPort == s.LocalMode.HTTPSPort {
		v.add(p+".localMode.httpsPort", CodeConflict, "http and https ports must differ")
	}
	switch s.LocalMode.ACMECA {
	case ACMELetsEncrypt, ACMELetsEncryptStaging, ACMEZeroSSL:
	default:
		v.add(p+".localMode.acmeCa", CodeInvalid, "must be letsencrypt, letsencrypt-staging or zerossl")
	}
	if e := s.LocalMode.ACMEEmail; e != "" && (!strings.Contains(e, "@") || strings.ContainsAny(e, " \r\n")) {
		v.add(p+".localMode.acmeEmail", CodeInvalid, "not an e-mail address")
	}
	if s.Events.RetentionDays < 1 || s.Events.RetentionDays > 365 {
		v.add(p+".events.retentionDays", CodeOutOfRange, "must be 1-365")
	}
	if s.Events.MaxSizeMB < 10 || s.Events.MaxSizeMB > 100000 {
		v.add(p+".events.maxSizeMb", CodeOutOfRange, "must be 10-100000")
	}
	switch s.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		v.add(p+".log.level", CodeInvalid, "must be debug, info, warn or error")
	}
	switch s.Log.Format {
	case "json", "text":
	default:
		v.add(p+".log.format", CodeInvalid, "must be json or text")
	}
	if l := s.Language; len(l) < 2 || len(l) > 5 || !isAlpha(strings.ReplaceAll(l, "-", "")) {
		v.add(p+".language", CodeInvalid, "must be a language tag like en or tr")
	}
}

func (v *validator) name(p, name string) {
	if name == "" {
		v.add(p, CodeRequired, "name is required")
	} else if len(name) > MaxNameLen {
		v.add(p, CodeTooLong, "at most %d characters", MaxNameLen)
	}
}

func isAlpha(s string) bool {
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return s != ""
}

var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateHostname accepts lowercase FQDNs (at least two labels), no
// wildcards, no IP literals.
func ValidateHostname(h string) error {
	if h == "" {
		return fmt.Errorf("hostname is empty")
	}
	if len(h) > MaxHostnameLen {
		return fmt.Errorf("hostname longer than %d characters", MaxHostnameLen)
	}
	if strings.Contains(h, "*") {
		return fmt.Errorf("wildcards are not allowed; add each hostname explicitly")
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return fmt.Errorf("must be a DNS name, not an IP address")
	}
	if h != strings.ToLower(h) {
		return fmt.Errorf("must be lowercase")
	}
	labels := strings.Split(strings.TrimSuffix(h, "."), ".")
	if len(labels) < 2 {
		return fmt.Errorf("must be a fully qualified name like app.example.com")
	}
	for _, l := range labels {
		if len(l) > MaxLabelLen {
			return fmt.Errorf("label %q longer than %d characters", l, MaxLabelLen)
		}
		if !labelRe.MatchString(l) {
			return fmt.Errorf("label %q contains invalid characters", l)
		}
	}
	return nil
}

func validateHostPort(target string) error {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	if host == "" {
		return fmt.Errorf("host is empty")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < MinPort || n > MaxPort {
		return fmt.Errorf("port must be 1-65535")
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	if host == "localhost" {
		return nil
	}
	if strings.ContainsAny(host, " /\\@") {
		return fmt.Errorf("host contains invalid characters")
	}
	return nil
}
