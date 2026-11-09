package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/kayaramazan/latch/latchd/internal/store"
	"github.com/kayaramazan/latch/proto/model"
)

var startedAt = time.Now()

func (s *Server) getConfig(w http.ResponseWriter, _ *http.Request) {
	snap, version := s.store.Current()
	writeJSON(w, http.StatusOK, map[string]any{"version": version, "snapshot": snap})
}

func (s *Server) listVersions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	before, _ := strconv.Atoi(r.URL.Query().Get("before"))
	vs, err := s.store.Versions(r.Context(), limit, before)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, vs)
}

func (s *Server) getVersion(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad version number")
		return
	}
	snap, info, err := s.store.Version(r.Context(), n)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"info": info, "snapshot": snap})
}

func (s *Server) getDiff(w http.ResponseWriter, r *http.Request) {
	a, _ := strconv.Atoi(r.URL.Query().Get("from"))
	b, _ := strconv.Atoi(r.URL.Query().Get("to"))
	diff, err := s.store.Diff(r.Context(), a, b)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func (s *Server) rollback(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad version number")
		return
	}
	res, err := s.store.Rollback(r.Context(), n, "api")
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": res.Version, "diff": res.Diff})
}

// validateConfig is the dry-run: it validates a proposed snapshot and reports
// field errors plus the routes that would change, without saving.
func (s *Server) validateConfig(w http.ResponseWriter, r *http.Request) {
	var proposed model.Snapshot
	if !decode(w, r, &proposed) {
		return
	}
	proposed.Normalize()
	resp := map[string]any{"valid": true}
	if err := model.Validate(&proposed); err != nil {
		var ve *model.ValidationError
		if errors.As(err, &ve) {
			resp["valid"] = false
			resp["fields"] = ve.Fields
		} else {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	current, _ := s.store.Current()
	diff := model.ComputeDiff(current, &proposed)
	resp["affectedRoutes"] = diff.AffectedRoutes
	resp["diff"] = diff
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) exportBackup(w http.ResponseWriter, _ *http.Request) {
	snap, version := s.store.Export()
	w.Header().Set("Content-Disposition", "attachment; filename=latch-backup.json")
	writeJSON(w, http.StatusOK, map[string]any{"version": version, "snapshot": snap})
}

func (s *Server) importBackup(w http.ResponseWriter, r *http.Request) {
	mode := store.ImportMode(r.URL.Query().Get("mode"))
	if mode == "" {
		mode = store.ImportReplace
	}
	var body struct {
		Version  int            `json:"version"`
		Snapshot model.Snapshot `json:"snapshot"`
	}
	if !decode(w, r, &body) {
		return
	}
	res, err := s.store.Import(r.Context(), &body.Snapshot, mode, "api")
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": res.Version, "diff": res.Diff})
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	snap, _ := s.store.Current()
	writeJSON(w, http.StatusOK, snap.Settings)
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var in model.Settings
	if !decode(w, r, &in) {
		return
	}
	res, err := s.store.Update(r.Context(), "api", "update settings", func(snap *model.Snapshot) error {
		// Preserve the password hash unless explicitly replaced (it is never
		// returned by GET, so clients cannot round-trip it).
		if in.Auth.PasswordHash == "" {
			in.Auth.PasswordHash = snap.Settings.Auth.PasswordHash
		}
		snap.Settings = in
		return nil
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res.Snapshot.Settings)
}

func (s *Server) getSystem(w http.ResponseWriter, _ *http.Request) {
	_, version := s.store.Current()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":       s.version,
		"configVersion": version,
		"uptimeSec":     int(time.Since(startedAt).Seconds()),
		"authRequired":  s.authEnabled(),
	})
}
