package control

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client calls a relay's control API over mTLS.
type Client struct {
	base string
	http *http.Client
}

// NewClient builds a control client that presents clientCert and trusts the
// relay's server certificate via relayCAs.
func NewClient(baseURL string, clientCert tls.Certificate, relayCAs *x509.CertPool) *Client {
	return &Client{
		base: baseURL,
		http: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					Certificates: []tls.Certificate{clientCert},
					RootCAs:      relayCAs,
					MinVersion:   tls.VersionTLS12,
				},
			},
		},
	}
}

// NewClientWithHTTP is for tests that supply their own *http.Client.
func NewClientWithHTTP(baseURL string, h *http.Client) *Client {
	return &Client{base: baseURL, http: h}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+APIVersion+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("control: %s %s: %s", method, path, e.Error)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// CreateToken mints an agent token on the relay.
func (c *Client) CreateToken(ctx context.Context, req CreateTokenRequest) (*CreateTokenResponse, error) {
	var out CreateTokenResponse
	if err := c.do(ctx, http.MethodPost, "/tokens", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTokens lists tokens.
func (c *Client) ListTokens(ctx context.Context) ([]TokenInfo, error) {
	var out []TokenInfo
	return out, c.do(ctx, http.MethodGet, "/tokens", nil, &out)
}

// RevokeToken revokes a token by id.
func (c *Client) RevokeToken(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/tokens/"+id, nil, nil)
}

// CreateDomain registers a custom domain.
func (c *Client) CreateDomain(ctx context.Context, req CreateDomainRequest) (*DomainInfo, error) {
	var out DomainInfo
	return &out, c.do(ctx, http.MethodPost, "/domains", req, &out)
}

// ListDomains lists custom domains.
func (c *Client) ListDomains(ctx context.Context) ([]DomainInfo, error) {
	var out []DomainInfo
	return out, c.do(ctx, http.MethodGet, "/domains", nil, &out)
}

// Health reads the relay's health + usage.
func (c *Client) Health(ctx context.Context) (*Health, error) {
	var out Health
	return &out, c.do(ctx, http.MethodGet, "/health", nil, &out)
}

// Sessions lists live agent sessions.
func (c *Client) Sessions(ctx context.Context) ([]SessionInfo, error) {
	var out []SessionInfo
	return out, c.do(ctx, http.MethodGet, "/sessions", nil, &out)
}
