// Package panel serves latch-control's minimal customer panel: signup/login and
// self-service token creation. A compact single page; the full product UI reuses
// latch-ui patterns later.
package panel

import (
	"embed"
	"net/http"
)

//go:embed static/index.html
var files embed.FS

// Handler serves the panel for any non-API path.
func Handler() http.Handler {
	index, _ := files.ReadFile("static/index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
