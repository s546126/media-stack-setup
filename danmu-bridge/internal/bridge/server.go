package bridge

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/s546126/danmu-bridge/internal/arr"
)

type job struct {
	req arr.ImportRequest
	at  time.Time
}

// Server receives webhooks, imports serially and runs the periodic sync.
type Server struct {
	Importer     *Importer
	Syncer       *Syncer
	Token        string
	ImportDelay  time.Duration
	SyncInterval time.Duration
	Log          *slog.Logger

	jobs    chan job
	syncNow chan struct{}
	mu      sync.Mutex
	pending map[string]string // request key -> episode spec queued
}

func (s *Server) init() {
	s.jobs = make(chan job, 1024)
	s.syncNow = make(chan struct{}, 1)
	s.pending = map[string]string{}
}

// Handler serves POST /webhook (also /webhook/sonarr, /webhook/radarr) and GET /healthz.
func (s *Server) Handler() http.Handler {
	if s.jobs == nil {
		s.init()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	mux.HandleFunc("POST /webhook", s.webhook)
	mux.HandleFunc("POST /webhook/{app}", s.webhook)
	return mux
}

func (s *Server) authorized(r *http.Request) bool {
	if s.Token == "" {
		return true
	}
	got := r.URL.Query().Get("token")
	if got == "" {
		// Sonarr/Radarr webhook "Username/Password" fields arrive as basic auth.
		_, got, _ = r.BasicAuth()
	}
	if got == "" {
		got = r.Header.Get("X-Webhook-Token")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	ev, err := arr.ParseWebhook(body)
	if err != nil {
		s.Log.Warn("bad webhook", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch {
	case ev.Type == "Test":
		s.Log.Info("webhook test received", "from", ev.Source)
	case ev.IsImport():
		for _, it := range ev.Items {
			s.Log.Info("imported", "from", ev.Source, "item", it.String(), "path", it.Path)
		}
		for _, req := range ev.Requests {
			s.enqueue(req)
		}
	case ev.TouchesFiles():
		s.Log.Info("files changed", "event", ev.Type, "from", ev.Source)
		s.TriggerSync()
	default:
		s.Log.Debug("ignored webhook", "event", ev.Type, "from", ev.Source)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) enqueue(req arr.ImportRequest) {
	k, spec := req.Key(), arr.EpisodeSpec(req.Episodes)
	s.mu.Lock()
	if s.pending[k] == spec {
		s.mu.Unlock()
		return
	}
	s.pending[k] = spec
	s.mu.Unlock()
	select {
	case s.jobs <- job{req: req, at: time.Now()}:
	default:
		s.Log.Error("import queue full, dropping; run backfill later to catch up", "item", req.String())
		s.mu.Lock()
		delete(s.pending, k)
		s.mu.Unlock()
	}
}

// TriggerSync asks the sync loop to run soon. Calls coalesce.
func (s *Server) TriggerSync() {
	select {
	case s.syncNow <- struct{}{}:
	default:
	}
}

// Run starts the import worker and sync loop and blocks until ctx ends.
func (s *Server) Run(ctx context.Context) {
	if s.jobs == nil {
		s.init()
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.importLoop(ctx) }()
	go func() { defer wg.Done(); s.syncLoop(ctx) }()
	wg.Wait()
}

func (s *Server) importLoop(ctx context.Context) {
	for {
		var j job
		select {
		case <-ctx.Done():
			return
		case j = <-s.jobs:
		}
		if wait := time.Until(j.at.Add(s.ImportDelay)); wait > 0 {
			if sleep(ctx, wait) != nil {
				return
			}
		}
		s.mu.Lock()
		delete(s.pending, j.req.Key())
		s.mu.Unlock()
		_, err := s.Importer.Run(ctx, j.req)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.Log.Error("import failed", "item", j.req.String(), "err", err)
		}
		s.TriggerSync()
	}
}

func (s *Server) syncLoop(ctx context.Context) {
	run := func() {
		start := time.Now()
		st, err := s.Syncer.RunOnce(ctx)
		if err != nil {
			s.Log.Error("sync failed", "err", err)
			return
		}
		s.Log.Info("sync done", "took", time.Since(start).Round(time.Millisecond),
			"items", st.Items, "written", st.Written, "unchanged", st.Unchanged,
			"notInMisaka", st.NotInMisaka, "noEpisode", st.NoEpisode, "empty", st.Empty,
			"removed", st.Removed, "errors", st.Errors)
	}
	run()
	t := time.NewTicker(s.SyncInterval)
	defer t.Stop()
	// Refresh cached Misaka metadata hourly so ids fixed in its UI are seen.
	refresh := time.NewTicker(time.Hour)
	defer refresh.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
			s.Syncer.ForgetDetails()
		case <-t.C:
			run()
		case <-s.syncNow:
			// Let a burst of webhooks settle before scanning.
			if sleep(ctx, 5*time.Second) != nil {
				return
			}
			run()
		}
	}
}
