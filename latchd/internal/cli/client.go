package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// apiClient talks to a running latchd's management API.
type apiClient struct {
	base string
	http *http.Client
}

func newAPIClient(base string) *apiClient {
	if base == "" {
		base = os.Getenv("LATCHD_API")
	}
	if base == "" {
		base = "http://127.0.0.1:4040"
	}
	base = strings.TrimRight(base, "/")
	return &apiClient{base: base, http: &http.Client{Timeout: 10 * time.Second}}
}

// apiError is a structured error body from the API.
type apiError struct {
	Error  string            `json:"error"`
	Fields []json.RawMessage `json:"fields"`
}

func (c *apiClient) do(method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach latchd at %s (is it running?): %w", c.base, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var ae apiError
		if json.Unmarshal(data, &ae) == nil && ae.Error != "" {
			if len(ae.Fields) > 0 {
				return fmt.Errorf("%s: %s", ae.Error, compactFields(ae.Fields))
			}
			return fmt.Errorf("%s", ae.Error)
		}
		return fmt.Errorf("API returned %s", resp.Status)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func compactFields(fields []json.RawMessage) string {
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		var fe struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		}
		if json.Unmarshal(f, &fe) == nil {
			parts = append(parts, fe.Path+" "+fe.Message)
		}
	}
	return strings.Join(parts, "; ")
}
