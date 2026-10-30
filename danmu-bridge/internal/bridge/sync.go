package bridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/s546126/danmu-bridge/internal/arr"
	"github.com/s546126/danmu-bridge/internal/danmaku"
	"github.com/s546126/danmu-bridge/internal/misaka"
	"github.com/s546126/danmu-bridge/internal/pathmap"
	"github.com/s546126/danmu-bridge/internal/state"
)

// Lister returns the video files one *arr instance knows about.
type Lister func(ctx context.Context) ([]arr.Item, error)

type FileState struct {
	Video     string    `json:"video"`
	EpisodeID int       `json:"episodeId"`
	Marker    string    `json:"marker"` // episodeId|fetchedAt|commentCount from Misaka
	SHA256    string    `json:"sha256"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type SyncState struct {
	// Files is keyed by the local xml path. Only xml this tool wrote is listed,
	// and only listed xml is ever deleted.
	Files map[string]*FileState `json:"files"`
}

type Syncer struct {
	Misaka        *misaka.Client
	Listers       []Lister
	Paths         *pathmap.Mapper
	StatePath     string
	RemoveOrphans bool
	Log           *slog.Logger

	mu      sync.Mutex // one cycle at a time
	details map[int]misaka.AnimeDetails
}

type SyncStats struct {
	Items, Written, Unchanged, NotInMisaka, NoEpisode, Empty, Removed, Errors int
}

// match is a Misaka library entry with its sources in preference order.
type match struct {
	animeID int
	title   string
	sources []int
}

// Index maps *arr ids to Misaka library entries.
type Index map[string]match

func tvKey(idType string, id string, season int) string {
	return "tv:" + idType + ":" + id + ":s" + strconv.Itoa(season)
}
func movieKey(idType, id string) string { return "movie:" + idType + ":" + id }

func (ix Index) add(k string, m match) {
	if _, dup := ix[k]; !dup {
		ix[k] = m
	}
}

// Lookup finds the Misaka entry for an item, trying tmdb, then tvdb/imdb.
func (ix Index) Lookup(it arr.Item) (match, bool) {
	var keys []string
	if it.Kind == arr.Movie {
		if it.TmdbID > 0 {
			keys = append(keys, movieKey("tmdb", strconv.Itoa(it.TmdbID)))
		}
		if it.ImdbID != "" {
			keys = append(keys, movieKey("imdb", it.ImdbID))
		}
	} else {
		if it.TmdbID > 0 {
			keys = append(keys, tvKey("tmdb", strconv.Itoa(it.TmdbID), it.Season))
		}
		if it.TvdbID > 0 {
			keys = append(keys, tvKey("tvdb", strconv.Itoa(it.TvdbID), it.Season))
		}
	}
	for _, k := range keys {
		if m, ok := ix[k]; ok {
			return m, true
		}
	}
	return match{}, false
}

// BuildIndex is exported for tests; it needs the library list and per-anime details.
func BuildIndex(lib []misaka.LibraryAnime, details map[int]misaka.AnimeDetails) Index {
	ix := Index{}
	for _, a := range lib {
		d, ok := details[a.AnimeID]
		if !ok {
			continue
		}
		m := match{animeID: a.AnimeID, title: a.Title}
		srcs := append([]misaka.SourceBrief(nil), a.Sources...)
		sort.SliceStable(srcs, func(i, j int) bool { return srcs[i].IsFavorited && !srcs[j].IsFavorited })
		for _, s := range srcs {
			m.sources = append(m.sources, s.SourceID)
		}
		typ := d.Type
		if typ == "" {
			typ = a.Type
		}
		if typ == "movie" {
			if d.TmdbID != "" {
				ix.add(movieKey("tmdb", d.TmdbID), m)
			}
			if d.ImdbID != "" {
				ix.add(movieKey("imdb", d.ImdbID), m)
			}
			continue
		}
		season := a.Season
		if season == 0 {
			season = d.Season
		}
		if d.TmdbID != "" {
			ix.add(tvKey("tmdb", d.TmdbID, season), m)
		}
		if d.TvdbID != "" {
			ix.add(tvKey("tvdb", d.TvdbID, season), m)
		}
	}
	return ix
}

func (s *Syncer) buildIndex(ctx context.Context) (Index, error) {
	lib, err := s.Misaka.Library(ctx)
	if err != nil {
		return nil, fmt.Errorf("misaka library: %w", err)
	}
	if s.details == nil {
		s.details = map[int]misaka.AnimeDetails{}
	}
	live := map[int]bool{}
	for _, a := range lib {
		live[a.AnimeID] = true
		if _, ok := s.details[a.AnimeID]; ok {
			continue
		}
		d, err := s.Misaka.Anime(ctx, a.AnimeID)
		if err != nil {
			s.Log.Warn("misaka anime details", "anime", a.AnimeID, "err", err)
			continue
		}
		s.details[a.AnimeID] = d
	}
	for id := range s.details {
		if !live[id] {
			delete(s.details, id)
		}
	}
	return BuildIndex(lib, s.details), nil
}

// ForgetDetails drops cached Misaka metadata so ids edited in Misaka's UI are picked up.
func (s *Syncer) ForgetDetails() {
	s.mu.Lock()
	s.details = nil
	s.mu.Unlock()
}

// RunOnce mirrors danmaku xml for every known video and removes orphans.
func (s *Syncer) RunOnce(ctx context.Context) (SyncStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st SyncStats

	var items []arr.Item
	for _, l := range s.Listers {
		its, err := l(ctx)
		if err != nil {
			st.Errors++
			s.Log.Error("list *arr items", "err", err)
			continue
		}
		items = append(items, its...)
	}
	ix, err := s.buildIndex(ctx)
	if err != nil {
		return st, err
	}

	ss := SyncState{Files: map[string]*FileState{}}
	if err := state.Load(s.StatePath, &ss); err != nil {
		return st, fmt.Errorf("load %s: %w", s.StatePath, err)
	}
	if ss.Files == nil {
		ss.Files = map[string]*FileState{}
	}

	episodes := map[int][]misaka.Episode{} // per-cycle cache by source id
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		st.Items++
		video := s.Paths.Local(it.Path)
		if video == "" {
			continue
		}
		if _, err := os.Lstat(video); err != nil {
			s.Log.Debug("video not visible here, check PATH_MAP and mounts", "video", video)
			continue
		}
		m, ok := ix.Lookup(it)
		if !ok {
			st.NotInMisaka++
			continue
		}
		ep, ok := s.findEpisode(ctx, m, it, episodes)
		if !ok {
			st.NoEpisode++
			continue
		}
		xmlPath := pathmap.XMLPath(video)
		wrote, err := s.syncOne(ctx, &ss, video, xmlPath, ep)
		switch {
		case err != nil:
			st.Errors++
			s.Log.Warn("sync xml", "item", it.String(), "xml", xmlPath, "err", err)
		case wrote == writeEmpty:
			st.Empty++
		case wrote == writeDone:
			st.Written++
			s.Log.Info("wrote danmaku", "item", it.String(), "xml", xmlPath, "comments", ep.CommentCount)
		default:
			st.Unchanged++
		}
	}

	if s.RemoveOrphans && ctx.Err() == nil {
		st.Removed = s.removeOrphans(&ss)
	}
	if err := state.Save(s.StatePath, &ss); err != nil {
		return st, fmt.Errorf("save state: %w", err)
	}
	return st, nil
}

func (s *Syncer) findEpisode(ctx context.Context, m match, it arr.Item, cache map[int][]misaka.Episode) (misaka.Episode, bool) {
	want := it.Episode
	if it.Kind == arr.Movie {
		want = 1
	}
	for _, sid := range m.sources {
		eps, ok := cache[sid]
		if !ok {
			var err error
			eps, err = s.Misaka.SourceEpisodes(ctx, sid)
			if err != nil {
				s.Log.Warn("misaka source episodes", "source", sid, "err", err)
				continue
			}
			cache[sid] = eps
		}
		for _, e := range eps {
			if e.EpisodeIndex == want {
				return e, true
			}
		}
		if it.Kind == arr.Movie && len(eps) == 1 {
			return eps[0], true
		}
	}
	return misaka.Episode{}, false
}

type writeResult int

const (
	writeSkipped writeResult = iota
	writeDone
	writeEmpty
)

func (s *Syncer) syncOne(ctx context.Context, ss *SyncState, video, xmlPath string, ep misaka.Episode) (writeResult, error) {
	marker := fmt.Sprintf("%d|%s|%d", ep.EpisodeID, ep.FetchedAt, ep.CommentCount)
	prev := ss.Files[xmlPath]
	_, statErr := os.Stat(xmlPath)
	exists := statErr == nil
	if prev != nil && prev.Marker == marker && prev.Video == video && exists {
		return writeSkipped, nil
	}
	if !exists && !errors.Is(statErr, fs.ErrNotExist) {
		return writeSkipped, statErr
	}
	if exists && prev == nil {
		// Someone else's xml (e.g. the Jellyfin danmaku plugin). Leave it.
		s.Log.Debug("xml exists and was not written by danmu-bridge, skipping", "xml", xmlPath)
		return writeSkipped, nil
	}

	comments, err := s.Misaka.Comments(ctx, ep.EpisodeID)
	if err != nil {
		return writeSkipped, err
	}
	if len(comments) == 0 {
		return writeEmpty, nil // retry next cycle; don't drop an empty file in the library
	}
	data := danmaku.RenderXML(ep.EpisodeID, comments)
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	res := writeSkipped
	if !exists || prev == nil || prev.SHA256 != hash || !sameContent(xmlPath, data) {
		if err := state.WriteFileAtomic(xmlPath, data, 0o644); err != nil {
			return writeSkipped, err
		}
		res = writeDone
	}
	ss.Files[xmlPath] = &FileState{Video: video, EpisodeID: ep.EpisodeID, Marker: marker, SHA256: hash, UpdatedAt: time.Now().UTC()}
	return res, nil
}

func sameContent(p string, data []byte) bool {
	b, err := os.ReadFile(p)
	return err == nil && bytes.Equal(b, data)
}

// removeOrphans deletes xml we wrote whose video is gone. Lstat is used so a
// symlink whose debrid target is temporarily unreachable still counts as present.
func (s *Syncer) removeOrphans(ss *SyncState) int {
	n := 0
	for xmlPath, f := range ss.Files {
		if _, err := os.Lstat(f.Video); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err := os.Remove(xmlPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.Log.Warn("remove orphan xml", "xml", xmlPath, "err", err)
			continue
		}
		s.Log.Info("removed orphan xml", "xml", xmlPath)
		delete(ss.Files, xmlPath)
		n++
	}
	return n
}
