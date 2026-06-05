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
	"sync"
	"time"

	"github.com/lendable/minimalist-cost-tracker/internal/db"
	"github.com/lendable/minimalist-cost-tracker/internal/pricing"
	"github.com/lendable/minimalist-cost-tracker/internal/selfupdate"
)

//go:embed static
var staticFS embed.FS

// versionCheckInterval is how often the dashboard re-polls GitHub for the
// latest release. Releases are infrequent, so a slow cadence is plenty and
// keeps the API quiet.
const versionCheckInterval = 6 * time.Hour

type Server struct {
	db      *db.DB
	pricer  *pricing.Pricer
	port    int
	version string // the running binary's version, surfaced via /api/version
	repo    string // GitHub owner/name to check for newer releases ("" disables)

	mu     sync.RWMutex
	latest string // most recent release tag seen by the background checker
}

func New(database *db.DB, pricer *pricing.Pricer, port int, version, repo string) *Server {
	return &Server{db: database, pricer: pricer, port: port, version: version, repo: repo}
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
	mux.HandleFunc("GET /api/profiles", s.handleProfiles)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/sessions", s.handleSessions)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleSessionByID)
	mux.HandleFunc("GET /api/skills", s.handleSkills)
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /api/timeline", s.handleTimeline)
	mux.HandleFunc("GET /api/version", s.handleVersion)

	return mux
}

func (s *Server) ListenAndServe() error {
	go s.watchVersion()
	addr := ":" + strconv.Itoa(s.port)
	log.Printf("cost-tracker dashboard: http://localhost%s", addr)
	return http.ListenAndServe(addr, s.Handler())
}

// watchVersion polls GitHub for the latest release on a slow cadence and caches
// it, so /api/version answers instantly without ever blocking on the network. A
// failed check is logged and retried on the next tick; it never affects serving.
func (s *Server) watchVersion() {
	if s.repo == "" {
		return
	}
	s.refreshLatest()
	ticker := time.NewTicker(versionCheckInterval)
	defer ticker.Stop()
	for range ticker.C {
		s.refreshLatest()
	}
}

func (s *Server) refreshLatest() {
	v, err := selfupdate.LatestVersion(s.repo)
	if err != nil {
		log.Printf("web: version check: %v", err)
		return
	}
	s.mu.Lock()
	s.latest = v
	s.mu.Unlock()
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

// handleProfiles lists the profiles that have recorded sessions, so the
// dashboard can offer a per-profile filter.
func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.db.Profiles()
	if err != nil {
		serverError(w, err)
		return
	}
	if profiles == nil {
		profiles = []string{}
	}
	writeJSON(w, profiles)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.db.Stats(r.URL.Query().Get("profile"))
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

	sessions, err := s.db.Sessions(limit, offset, sortBy, q.Get("profile"))
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
	skills, err := s.db.Skills(r.URL.Query().Get("profile"))
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, skills)
}

// handleModels merges raw per-model rows into family buckets using the pricer's
// normaliser, so the breakdown chart groups e.g. sonnet-4-5 and sonnet-4-6.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Models(r.URL.Query().Get("profile"))
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
	buckets, err := s.db.Timeline(days, r.URL.Query().Get("profile"))
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, buckets)
}

// versionInfo is the /api/version payload the dashboard polls to decide whether
// to show an "update available" banner.
type versionInfo struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
}

// handleVersion reports the running version and, once the background checker has
// populated it, the latest release and whether an update is available.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	latest := s.latest
	s.mu.RUnlock()

	info := versionInfo{Current: s.version, Latest: latest}
	if latest != "" {
		info.UpdateAvailable = selfupdate.IsNewer(s.version, latest)
	}
	writeJSON(w, info)
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
