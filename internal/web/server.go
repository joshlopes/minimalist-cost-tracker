// Package web serves the read-only dashboard: an embedded single-page app plus
// a small JSON API backed by the db query methods.
package web

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strconv"

	"github.com/lendable/minimalist-cost-tracker/internal/db"
	"github.com/lendable/minimalist-cost-tracker/internal/pricing"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	db     *db.DB
	pricer *pricing.Pricer
	port   int
}

func New(database *db.DB, pricer *pricing.Pricer, port int) *Server {
	return &Server{db: database, pricer: pricer, port: port}
}

// Handler builds the route table. Exposed separately from ListenAndServe so it
// can be exercised by tests via httptest.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // embed paths are compile-time constant; cannot fail at runtime
	}
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("GET /static/", http.StripPrefix("/static/", fileServer))

	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/sessions", s.handleSessions)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleSessionByID)
	mux.HandleFunc("GET /api/skills", s.handleSkills)
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /api/timeline", s.handleTimeline)

	return mux
}

func (s *Server) ListenAndServe() error {
	addr := ":" + strconv.Itoa(s.port)
	log.Printf("cost-tracker dashboard: http://localhost%s", addr)
	return http.ListenAndServe(addr, s.Handler())
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.db.Stats()
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, stats)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := atoiDefault(q.Get("limit"), 50)
	offset := atoiDefault(q.Get("offset"), 0)
	sortBy := q.Get("sort")

	sessions, err := s.db.Sessions(limit, offset, sortBy)
	if err != nil {
		serverError(w, err)
		return
	}
	if sessions == nil {
		sessions = []db.SessionRow{}
	}
	writeJSON(w, sessions)
}

func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	detail, err := s.db.SessionByID(id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, detail)
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	skills, err := s.db.Skills()
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, skills)
}

// handleModels merges raw per-model rows into family buckets using the pricer's
// normaliser, so the breakdown chart groups e.g. sonnet-4-5 and sonnet-4-6.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Models()
	if err != nil {
		serverError(w, err)
		return
	}
	byFamily := map[string]*db.ModelStatRow{}
	for _, row := range rows {
		family := row.Model
		if family != "unknown" {
			if n := s.pricer.Normalize(row.Model); n != "" {
				family = n
			}
		}
		agg, ok := byFamily[family]
		if !ok {
			agg = &db.ModelStatRow{Model: family}
			byFamily[family] = agg
		}
		agg.SessionCount += row.SessionCount
		agg.TotalCostUSD += row.TotalCostUSD
		agg.TotalInput += row.TotalInput
		agg.TotalOutput += row.TotalOutput
	}
	out := make([]db.ModelStatRow, 0, len(byFamily))
	for _, agg := range byFamily {
		out = append(out, *agg)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].TotalCostUSD > out[j].TotalCostUSD
	})
	writeJSON(w, out)
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	days := atoiDefault(r.URL.Query().Get("days"), 30)
	buckets, err := s.db.Timeline(days)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, buckets)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("web: encode json: %v", err)
	}
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("web: %v", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
