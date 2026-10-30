package arr

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
)

// Event is a parsed Sonarr or Radarr webhook.
type Event struct {
	Type     string // eventType as sent: Test, Download, Rename, EpisodeFileDelete, ...
	Source   string // "sonarr" or "radarr", detected from the payload
	Items    []Item // imported files with their episode numbers (Download only)
	Requests []ImportRequest
}

// IsImport reports whether the event is a completed import ("On Import" /
// "On Upgrade" in the UI; both are sent as eventType Download).
func (e Event) IsImport() bool { return e.Type == "Download" }

// TouchesFiles reports events after which the sidecar xml may be stale.
func (e Event) TouchesFiles() bool {
	switch e.Type {
	case "Download", "Rename", "EpisodeFileDelete", "SeriesDelete", "MovieFileDelete", "MovieDelete":
		return true
	}
	return false
}

type whFile struct {
	Path         string `json:"path"`
	RelativePath string `json:"relativePath"`
}

type whEpisode struct {
	SeasonNumber  int `json:"seasonNumber"`
	EpisodeNumber int `json:"episodeNumber"`
}

type whPayload struct {
	EventType string `json:"eventType"`
	Series    *struct {
		Title  string `json:"title"`
		Path   string `json:"path"`
		TvdbID int    `json:"tvdbId"`
		TmdbID int    `json:"tmdbId"`
		ImdbID string `json:"imdbId"`
	} `json:"series"`
	Episodes []whEpisode `json:"episodes"`
	// Per-file Download event.
	EpisodeFile *whFile `json:"episodeFile"`
	// Sonarr v4 "import complete" Download event carries a list instead.
	EpisodeFiles []whFile `json:"episodeFiles"`

	Movie *struct {
		Title      string `json:"title"`
		FolderPath string `json:"folderPath"`
		TmdbID     int    `json:"tmdbId"`
		ImdbID     string `json:"imdbId"`
	} `json:"movie"`
	MovieFile *whFile `json:"movieFile"`
}

// ParseWebhook decodes a Sonarr v4 / Radarr v5 webhook body.
func ParseWebhook(body []byte) (Event, error) {
	var p whPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, fmt.Errorf("decode webhook: %w", err)
	}
	if p.EventType == "" {
		return Event{}, fmt.Errorf("webhook has no eventType")
	}
	ev := Event{Type: p.EventType}
	switch {
	case p.Series != nil:
		ev.Source = "sonarr"
	case p.Movie != nil:
		ev.Source = "radarr"
	}
	if !ev.IsImport() {
		return ev, nil
	}

	switch ev.Source {
	case "sonarr":
		s := p.Series
		base := Item{Kind: TV, Title: s.Title, TmdbID: s.TmdbID, TvdbID: s.TvdbID, ImdbID: s.ImdbID}
		ev.Items = sonarrItems(base, s.Path, p.Episodes, p.EpisodeFile, p.EpisodeFiles)
		if len(ev.Items) == 0 {
			return ev, fmt.Errorf("sonarr Download event without episodes")
		}
	case "radarr":
		m := p.Movie
		it := Item{Kind: Movie, Title: m.Title, TmdbID: m.TmdbID, ImdbID: m.ImdbID}
		if p.MovieFile != nil {
			it.Path = filePath(m.FolderPath, *p.MovieFile)
		}
		ev.Items = []Item{it}
	default:
		return ev, fmt.Errorf("Download event has neither series nor movie")
	}
	ev.Requests = GroupRequests(ev.Items)
	return ev, nil
}

func filePath(root string, f whFile) string {
	if f.Path != "" {
		return f.Path
	}
	if f.RelativePath != "" && root != "" {
		return path.Join(root, f.RelativePath)
	}
	return ""
}

var sxxexx = regexp.MustCompile(`(?i)S(\d{1,3})E(\d{1,4})`)

func sonarrItems(base Item, seriesPath string, eps []whEpisode, one *whFile, many []whFile) []Item {
	var items []Item
	if one != nil {
		p := filePath(seriesPath, *one)
		for _, e := range eps {
			it := base
			it.Season, it.Episode, it.Path = e.SeasonNumber, e.EpisodeNumber, p
			items = append(items, it)
		}
		return items
	}
	// Import-complete: files and episodes are separate lists. Pair them by the
	// SxxEyy in the file name; episodes we can't pair are still imported, just
	// without a path (the periodic sync finds the path from the API later).
	byKey := map[[2]int]string{}
	for _, f := range many {
		p := filePath(seriesPath, f)
		if m := sxxexx.FindStringSubmatch(path.Base(p)); m != nil {
			s, _ := strconv.Atoi(m[1])
			e, _ := strconv.Atoi(m[2])
			byKey[[2]int{s, e}] = p
		}
	}
	for _, e := range eps {
		it := base
		it.Season, it.Episode = e.SeasonNumber, e.EpisodeNumber
		it.Path = byKey[[2]int{e.SeasonNumber, e.EpisodeNumber}]
		items = append(items, it)
	}
	return items
}
