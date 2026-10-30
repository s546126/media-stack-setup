// Package arr talks to Sonarr v4 and Radarr v5: webhook payloads in, API
// listings out, both reduced to the same Item shape.
package arr

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type Kind string

const (
	TV    Kind = "tv"
	Movie Kind = "movie"
)

// Item is one video file on disk and the ids needed to find its danmaku.
type Item struct {
	Kind    Kind
	Title   string
	TmdbID  int
	TvdbID  int
	ImdbID  string
	Season  int // 0 for movies
	Episode int // 0 for movies
	Path    string
}

func (it Item) String() string {
	if it.Kind == Movie {
		return fmt.Sprintf("%s (tmdb %d)", it.Title, it.TmdbID)
	}
	return fmt.Sprintf("%s S%02dE%02d", it.Title, it.Season, it.Episode)
}

// ImportRequest is one Misaka auto-import call: a movie, or a set of episodes
// of one season of one series.
type ImportRequest struct {
	Kind     Kind
	Title    string
	TmdbID   int
	TvdbID   int
	ImdbID   string
	Season   int
	Episodes []int
}

// Key identifies the request for dedupe and backfill progress.
func (r ImportRequest) Key() string {
	if r.Kind == Movie {
		return fmt.Sprintf("movie:tmdb%d:imdb%s", r.TmdbID, r.ImdbID)
	}
	return fmt.Sprintf("tv:tvdb%d:tmdb%d:s%d", r.TvdbID, r.TmdbID, r.Season)
}

func (r ImportRequest) String() string {
	if r.Kind == Movie {
		return fmt.Sprintf("%s (tmdb %d)", r.Title, r.TmdbID)
	}
	return fmt.Sprintf("%s S%02d E[%s]", r.Title, r.Season, EpisodeSpec(r.Episodes))
}

// EpisodeSpec renders episode numbers the way Misaka's `episode` query wants
// them: sorted, deduped, consecutive runs collapsed, e.g. "1-3,5,7-8".
func EpisodeSpec(eps []int) string {
	if len(eps) == 0 {
		return ""
	}
	s := append([]int(nil), eps...)
	sort.Ints(s)
	var parts []string
	start, prev := s[0], s[0]
	flush := func() {
		if start == prev {
			parts = append(parts, strconv.Itoa(start))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", start, prev))
		}
	}
	for _, e := range s[1:] {
		switch {
		case e == prev:
			continue
		case e == prev+1:
			prev = e
		default:
			flush()
			start, prev = e, e
		}
	}
	flush()
	return strings.Join(parts, ",")
}

// GroupRequests turns items into import requests: one per movie, one per
// series season. Episode 0 (specials placeholders) is dropped.
func GroupRequests(items []Item) []ImportRequest {
	idx := map[string]int{}
	var out []ImportRequest
	for _, it := range items {
		r := ImportRequest{Kind: it.Kind, Title: it.Title, TmdbID: it.TmdbID, TvdbID: it.TvdbID, ImdbID: it.ImdbID}
		if it.Kind == TV {
			if it.Episode <= 0 {
				continue
			}
			r.Season = it.Season
		}
		k := r.Key()
		if i, ok := idx[k]; ok {
			if it.Kind == TV {
				out[i].Episodes = append(out[i].Episodes, it.Episode)
			}
			continue
		}
		if it.Kind == TV {
			r.Episodes = []int{it.Episode}
		}
		idx[k] = len(out)
		out = append(out, r)
	}
	for i := range out {
		if out[i].Kind == TV {
			out[i].Episodes = uniqSorted(out[i].Episodes)
		}
	}
	return out
}

func uniqSorted(in []int) []int {
	sort.Ints(in)
	out := in[:0]
	for i, v := range in {
		if i == 0 || v != in[i-1] {
			out = append(out, v)
		}
	}
	return out
}
