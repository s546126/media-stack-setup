package bridge

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/s546126/danmu-bridge/internal/arr"
	"github.com/s546126/danmu-bridge/internal/misaka"
	"github.com/s546126/danmu-bridge/internal/pathmap"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeMisaka serves the subset of /api/control the bridge uses.
type fakeMisaka struct {
	mu           sync.Mutex
	commentCalls int
	fetchedAt    string
	imports      []string
	importStatus []int // status codes to return in order; then 202
}

func (f *fakeMisaka) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-API-Key") != "k" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	js := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	mux.HandleFunc("GET /api/control/library", auth(func(w http.ResponseWriter, r *http.Request) {
		js(w, []map[string]any{
			{"animeId": 1, "title": "Show", "type": "tv_series", "season": 1,
				"sources": []map[string]any{{"sourceId": 10, "isFavorited": false}, {"sourceId": 11, "isFavorited": true}}},
			{"animeId": 2, "title": "Film", "type": "movie", "season": 1,
				"sources": []map[string]any{{"sourceId": 20}}},
		})
	}))
	mux.HandleFunc("GET /api/control/library/anime/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("id") {
		case "1":
			js(w, map[string]any{"title": "Show", "type": "tv_series", "season": 1, "tmdbId": "200", "tvdbId": "100"})
		case "2":
			js(w, map[string]any{"title": "Film", "type": "movie", "season": 1, "tmdbId": "300"})
		default:
			w.WriteHeader(404)
		}
	}))
	mux.HandleFunc("GET /api/control/library/source/{id}/episodes", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		fa := f.fetchedAt
		f.mu.Unlock()
		switch r.PathValue("id") {
		case "10":
			t.Errorf("non-favorited source queried although favorited one has the episode")
			js(w, []any{})
		case "11":
			js(w, []map[string]any{{"episodeId": 111, "episodeIndex": 1, "fetchedAt": fa, "commentCount": 2}})
		case "20":
			js(w, []map[string]any{{"episodeId": 201, "episodeIndex": 1, "fetchedAt": fa, "commentCount": 1}})
		}
	}))
	mux.HandleFunc("GET /api/control/danmaku/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.commentCalls++
		f.mu.Unlock()
		js(w, map[string]any{"count": 2, "comments": []map[string]any{
			{"cid": 2, "p": "12.5,1,25,16777215,1700000000,0,[bilibili],2,0", "m": "后面 & <b>"},
			{"cid": 1, "p": "1.0,1,25,16777215,1700000000,0,[bilibili],1,0", "m": "前面\x01"},
		}})
	}))
	mux.HandleFunc("POST /api/control/import/auto", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.imports = append(f.imports, r.URL.RawQuery)
		if len(f.importStatus) > 0 {
			code := f.importStatus[0]
			f.importStatus = f.importStatus[1:]
			w.WriteHeader(code)
			js(w, map[string]any{"detail": "busy"})
			return
		}
		w.WriteHeader(http.StatusAccepted)
		js(w, map[string]any{"message": "ok", "taskId": "t1"})
	}))
	mux.HandleFunc("GET /api/control/tasks/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		js(w, map[string]any{"taskId": "t1", "status": misaka.StatusCompleted, "description": "done"})
	}))
	return mux
}

func TestSyncWritesIdempotentAndRemovesOrphans(t *testing.T) {
	fm := &fakeMisaka{fetchedAt: "2026-10-30T00:00:00"}
	ts := httptest.NewServer(fm.handler(t))
	defer ts.Close()

	lib := t.TempDir()
	// Library paths as Sonarr/Radarr see them live under /arr; PATH_MAP maps to lib.
	tvVideo := filepath.Join(lib, "tv/Show/Season 01/Show - S01E01.mkv")
	movieVideo := filepath.Join(lib, "movies/Film (2020)/Film.mp4")
	for _, p := range []string{tvVideo, movieVideo} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The tv video is a dangling symlink, like a Decypharr link whose mount is down.
	if err := os.Symlink("/nonexistent/debrid/file.mkv", tvVideo); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(movieVideo, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	items := []arr.Item{
		{Kind: arr.TV, Title: "Show", TvdbID: 100, Season: 1, Episode: 1, Path: "/arr/tv/Show/Season 01/Show - S01E01.mkv"},
		{Kind: arr.Movie, Title: "Film", TmdbID: 300, Path: "/arr/movies/Film (2020)/Film.mp4"},
		{Kind: arr.TV, Title: "Unknown", TvdbID: 999, Season: 1, Episode: 1, Path: "/arr/tv/Show/Season 01/Show - S01E01.mkv"},
	}
	pm, _ := pathmap.Parse("/arr:" + lib)
	s := &Syncer{
		Misaka:    &misaka.Client{BaseURL: ts.URL, APIKey: "k", HTTP: ts.Client()},
		Listers:   []Lister{func(context.Context) ([]arr.Item, error) { return items, nil }},
		Paths:     pm,
		StatePath: filepath.Join(t.TempDir(), "state.json"), RemoveOrphans: true, Log: quiet,
	}
	ctx := context.Background()

	st, err := s.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Written != 2 || st.NotInMisaka != 1 {
		t.Fatalf("first run stats %+v", st)
	}
	tvXML := pathmap.XMLPath(tvVideo)
	b, err := os.ReadFile(tvXML)
	if err != nil {
		t.Fatal(err)
	}
	x := string(b)
	if !strings.Contains(x, "<chatid>111</chatid>") || strings.Index(x, "前面") > strings.Index(x, "后面") ||
		!strings.Contains(x, "后面 &amp; &lt;b&gt;") || strings.Contains(x, "\x01") {
		t.Fatalf("unexpected xml:\n%s", x)
	}
	if fi, _ := os.Lstat(tvVideo); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("video symlink was replaced")
	}

	// Second run: nothing changed in Misaka, so comments are not refetched.
	calls := fm.commentCalls
	st, err = s.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Written != 0 || st.Unchanged != 2 || fm.commentCalls != calls {
		t.Fatalf("second run stats %+v calls %d->%d", st, calls, fm.commentCalls)
	}

	// Misaka refreshed the episode but comments are identical: refetch, no rewrite.
	fm.mu.Lock()
	fm.fetchedAt = "2026-10-31T00:00:00"
	fm.mu.Unlock()
	before, _ := os.Stat(tvXML)
	time.Sleep(10 * time.Millisecond)
	st, _ = s.RunOnce(ctx)
	after, _ := os.Stat(tvXML)
	if st.Written != 0 || fm.commentCalls != calls+2 || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("third run stats %+v", st)
	}

	// Movie deleted: its xml goes, the tv xml stays.
	if err := os.Remove(movieVideo); err != nil {
		t.Fatal(err)
	}
	st, _ = s.RunOnce(ctx)
	if st.Removed != 1 {
		t.Fatalf("orphan stats %+v", st)
	}
	if _, err := os.Stat(pathmap.XMLPath(movieVideo)); !os.IsNotExist(err) {
		t.Fatal("movie xml not removed")
	}
	if _, err := os.Stat(tvXML); err != nil {
		t.Fatal("tv xml removed")
	}
	// No temp files left behind.
	m, _ := filepath.Glob(filepath.Join(filepath.Dir(tvXML), ".*tmp*"))
	if len(m) != 0 {
		t.Fatalf("temp files left: %v", m)
	}
}

func TestSyncLeavesForeignXML(t *testing.T) {
	fm := &fakeMisaka{}
	ts := httptest.NewServer(fm.handler(t))
	defer ts.Close()
	lib := t.TempDir()
	video := filepath.Join(lib, "Film.mp4")
	_ = os.WriteFile(video, []byte("x"), 0o644)
	_ = os.WriteFile(pathmap.XMLPath(video), []byte("theirs"), 0o644)
	pm, _ := pathmap.Parse("")
	s := &Syncer{
		Misaka: &misaka.Client{BaseURL: ts.URL, APIKey: "k", HTTP: ts.Client()},
		Listers: []Lister{func(context.Context) ([]arr.Item, error) {
			return []arr.Item{{Kind: arr.Movie, TmdbID: 300, Path: video}}, nil
		}},
		Paths: pm, StatePath: filepath.Join(t.TempDir(), "s.json"), RemoveOrphans: true, Log: quiet,
	}
	if _, err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(pathmap.XMLPath(video)); string(b) != "theirs" {
		t.Fatal("foreign xml overwritten")
	}
}

func TestImporterRetriesBusyAndTreatsConflictAsDone(t *testing.T) {
	fm := &fakeMisaka{importStatus: []int{429, 503}}
	ts := httptest.NewServer(fm.handler(t))
	defer ts.Close()
	im := &Importer{
		Misaka: &misaka.Client{BaseURL: ts.URL, APIKey: "k", HTTP: ts.Client()}, TVIDSource: "tmdb",
		TaskWait: time.Second, PollEvery: time.Millisecond, RetryBase: time.Millisecond, Log: quiet,
	}
	req := arr.ImportRequest{Kind: arr.TV, Title: "Show", TmdbID: 200, TvdbID: 100, Season: 2, Episodes: []int{1, 2, 3, 7}}
	out, err := im.Run(context.Background(), req)
	if err != nil || out != Submitted {
		t.Fatalf("out=%v err=%v", out, err)
	}
	if len(fm.imports) != 3 {
		t.Fatalf("imports %v", fm.imports)
	}
	if q := fm.imports[2]; !strings.Contains(q, "searchType=tmdb") || !strings.Contains(q, "searchTerm=200") ||
		!strings.Contains(q, "season=2") || !strings.Contains(q, "episode=1-3%2C7") || !strings.Contains(q, "mediaType=tv_series") {
		t.Fatalf("query %s", q)
	}

	fm.importStatus = []int{409}
	if out, err := im.Run(context.Background(), req); err != nil || out != Duplicate {
		t.Fatalf("409: out=%v err=%v", out, err)
	}
	fm.importStatus = []int{400}
	if _, err := im.Run(context.Background(), req); err == nil || !strings.Contains(err.Error(), "permanent") {
		t.Fatalf("400: err=%v", err)
	}
}

func TestRequestIDChoice(t *testing.T) {
	r := arr.ImportRequest{Kind: arr.TV, TmdbID: 0, TvdbID: 100, Season: 1, Episodes: []int{1}}
	if a, _ := Request(r, "tmdb"); a.SearchType != "tvdb" || a.SearchTerm != "100" {
		t.Fatalf("fallback to tvdb: %+v", a)
	}
	r.TmdbID = 200
	if a, _ := Request(r, "tvdb"); a.SearchType != "tvdb" {
		t.Fatalf("prefer tvdb: %+v", a)
	}
	m := arr.ImportRequest{Kind: arr.Movie, ImdbID: "tt1"}
	if a, _ := Request(m, "tmdb"); a.SearchType != "imdb" || a.MediaType != "movie" || a.Season != 0 {
		t.Fatalf("movie imdb: %+v", a)
	}
	if _, err := Request(arr.ImportRequest{Kind: arr.Movie}, "tmdb"); err == nil {
		t.Fatal("want error for movie without ids")
	}
}

func TestWebhookAuthAndQueue(t *testing.T) {
	srv := &Server{Token: "s3cret", Log: quiet}
	h := srv.Handler()
	body, _ := os.ReadFile("../arr/testdata/sonarr_download.json")

	post := func(url string, user, pass string) int {
		req := httptest.NewRequest("POST", url, strings.NewReader(string(body)))
		if pass != "" {
			req.SetBasicAuth(user, pass)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := post("/webhook", "", ""); c != 401 {
		t.Fatalf("no token: %d", c)
	}
	if c := post("/webhook?token=nope", "", ""); c != 401 {
		t.Fatalf("bad token: %d", c)
	}
	if c := post("/webhook/sonarr?token=s3cret", "", ""); c != 204 {
		t.Fatalf("query token: %d", c)
	}
	if c := post("/webhook", "sonarr", "s3cret"); c != 204 {
		t.Fatalf("basic auth: %d", c)
	}
	// Same request twice while pending is queued once.
	if n := len(srv.jobs); n != 1 {
		t.Fatalf("queued %d jobs, want 1", n)
	}
}
