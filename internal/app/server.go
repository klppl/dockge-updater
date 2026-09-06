package app

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type Server struct {
	manager *Manager
	assets  fs.FS
	logger  *slog.Logger
}

func NewServer(manager *Manager, assets fs.FS, logger *slog.Logger) http.Handler {
	server := &Server{manager: manager, assets: assets, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", server.status)
	mux.HandleFunc("POST /api/check", server.check)
	mux.HandleFunc("PUT /api/settings", server.settings)
	mux.HandleFunc("POST /api/stacks/{id}/update", server.updateStack)
	mux.HandleFunc("GET /healthz", server.health)
	mux.Handle("/", http.FileServerFS(assets))
	return server.headers(server.logging(mux))
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.manager.Snapshot(time.Now()))
}

func (s *Server) check(w http.ResponseWriter, _ *http.Request) {
	if err := s.manager.CheckAllAsync(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	var settings Settings
	if err := decoder.Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, "invalid settings payload")
		return
	}
	if err := s.manager.UpdateSettings(settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateStack(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := s.manager.UpdateStackAsync(id); err != nil {
		status := http.StatusConflict
		if err.Error() == "stack not found" {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self' https://fonts.googleapis.com https://fonts.gstatic.com; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			s.logger.Debug("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func IsServerClosed(err error) bool {
	return errors.Is(err, http.ErrServerClosed)
}
