package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// relaySetupScript returns the one-line command to install a relay on a VPS.
// It does not run anything; the UI shows it for the operator to paste.
func (s *Server) relaySetupScript(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PublicHost string `json:"publicHost"`
		Email      string `json:"email"`
		BaseURL    string `json:"baseUrl"` // optional override for the installer URL
	}
	if !decode(w, r, &in) {
		return
	}
	base := in.BaseURL
	if base == "" {
		base = "https://github.com/kayaramazan/latch/releases/latest/download/install.sh"
	}
	cmd := fmt.Sprintf("curl -sSL %s | sh", base)
	wizard := "sudo latch-relay install"
	if in.PublicHost != "" {
		wizard += " --public-host " + shellQuote(in.PublicHost)
	}
	if in.Email != "" {
		wizard += " --email " + shellQuote(in.Email)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"install":     cmd,
		"wizard":      wizard,
		"explanation": "Run this on a public-IP server. The wizard obtains certificates, writes a systemd unit and prints a connection string to paste back here.",
	})
}

// relayDNSCheck resolves a hostname and reports its addresses, so the UI can
// tell the operator whether DNS points at their relay yet.
func (s *Server) relayDNSCheck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hostname string `json:"hostname"`
		ExpectIP string `json:"expectIp"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Hostname == "" {
		writeErr(w, http.StatusBadRequest, "hostname is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, in.Hostname)
	resp := map[string]any{"hostname": in.Hostname, "addresses": addrs, "resolves": err == nil && len(addrs) > 0}
	if err != nil {
		resp["error"] = err.Error()
	}
	if in.ExpectIP != "" {
		matched := false
		for _, a := range addrs {
			if a == in.ExpectIP {
				matched = true
			}
		}
		resp["pointsHere"] = matched
	}
	writeJSON(w, http.StatusOK, resp)
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
