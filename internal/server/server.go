// Package server wires up HTTP routes and renders the web UI.
package server

import (
	"embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"time"
)

// The //go:embed directive compiles the templates into the binary, so the
// static build needs no files next to it. It must sit directly above the var.
//
//go:embed templates/*.html
var templateFS embed.FS

// AppName is the display name used in page titles and the UI.
const AppName = "Höðrs Hoard"

// Server holds what the HTTP handlers need. Later milestones add the
// database and other dependencies here.
type Server struct {
	version   string
	started   time.Time
	log       *slog.Logger
	templates *template.Template
}

// New parses the embedded templates and returns a ready Server.
func New(version string, log *slog.Logger) (*Server, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		version:   version,
		started:   time.Now(),
		log:       log,
		templates: tmpl,
	}, nil
}

// Handler returns the router with all routes registered.
//
// Since Go 1.22 the standard ServeMux understands methods and wildcards in
// patterns ("GET /items/{id}"), so no router library is needed.
// "GET /{$}" matches exactly "/" and nothing below it.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	return s.logRequests(mux)
}

// health is the JSON body of /healthz. The `json:"..."` struct tags set the
// key names used when encoding.
type health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Uptime  string `json:"uptime"`
}

func (s *Server) health() health {
	return health{
		Status:  "ok",
		Version: s.version,
		Uptime:  time.Since(s.started).Round(time.Second).String(),
	}
}

// handleHealth is for monitoring and container health checks.
// The method receiver `(s *Server)` makes this a method on Server.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(s.health()); err != nil {
		s.log.Error("write health response", "err", err)
	}
}

// handleIndex renders the start page, which for now shows the health status.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data := struct {
		AppName string
		Health  health
	}{AppName, s.health()}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		s.log.Error("render index", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// statusRecorder wraps a ResponseWriter to remember the status code.
// Embedding http.ResponseWriter (a field with no name) means all its methods
// are available on statusRecorder; we only override WriteHeader.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// logRequests is middleware: it wraps a handler and logs each request.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Debug("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start),
		)
	})
}
