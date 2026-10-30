package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
)

// Client is a minimal Sonarr/Radarr v3 API client (both apps serve /api/v3).
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func (c *Client) get(ctx context.Context, p string, q url.Values, out any) error {
	u := c.BaseURL + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: %s: %s", p, resp.Status, b)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type sonarrSeries struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	TvdbID int    `json:"tvdbId"`
	TmdbID int    `json:"tmdbId"`
	ImdbID string `json:"imdbId"`
}

type sonarrEpisode struct {
	SeasonNumber  int  `json:"seasonNumber"`
	EpisodeNumber int  `json:"episodeNumber"`
	EpisodeFileID int  `json:"episodeFileId"`
	HasFile       bool `json:"hasFile"`
}

type sonarrEpisodeFile struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}

// SonarrItems lists every episode that has a file.
func (c *Client) SonarrItems(ctx context.Context) ([]Item, error) {
	var series []sonarrSeries
	if err := c.get(ctx, "/api/v3/series", nil, &series); err != nil {
		return nil, err
	}
	var items []Item
	for _, s := range series {
		q := url.Values{"seriesId": {fmt.Sprint(s.ID)}}
		var files []sonarrEpisodeFile
		if err := c.get(ctx, "/api/v3/episodefile", q, &files); err != nil {
			return nil, err
		}
		if len(files) == 0 {
			continue
		}
		var eps []sonarrEpisode
		if err := c.get(ctx, "/api/v3/episode", q, &eps); err != nil {
			return nil, err
		}
		items = append(items, joinSonarr(s, eps, files)...)
	}
	return items, nil
}

// joinSonarr pairs episodes with their files. A multi-episode file maps to
// its lowest episode number only, because one video can have one sidecar xml.
func joinSonarr(s sonarrSeries, eps []sonarrEpisode, files []sonarrEpisodeFile) []Item {
	paths := make(map[int]string, len(files))
	for _, f := range files {
		paths[f.ID] = f.Path
	}
	sort.Slice(eps, func(i, j int) bool {
		if eps[i].SeasonNumber != eps[j].SeasonNumber {
			return eps[i].SeasonNumber < eps[j].SeasonNumber
		}
		return eps[i].EpisodeNumber < eps[j].EpisodeNumber
	})
	seen := map[int]bool{}
	var items []Item
	for _, e := range eps {
		p, ok := paths[e.EpisodeFileID]
		if !e.HasFile || !ok || p == "" || seen[e.EpisodeFileID] {
			continue
		}
		seen[e.EpisodeFileID] = true
		items = append(items, Item{
			Kind: TV, Title: s.Title, TmdbID: s.TmdbID, TvdbID: s.TvdbID, ImdbID: s.ImdbID,
			Season: e.SeasonNumber, Episode: e.EpisodeNumber, Path: p,
		})
	}
	return items
}

type radarrMovie struct {
	Title     string `json:"title"`
	TmdbID    int    `json:"tmdbId"`
	ImdbID    string `json:"imdbId"`
	HasFile   bool   `json:"hasFile"`
	MovieFile *struct {
		Path string `json:"path"`
	} `json:"movieFile"`
}

// RadarrItems lists every movie that has a file.
func (c *Client) RadarrItems(ctx context.Context) ([]Item, error) {
	var movies []radarrMovie
	if err := c.get(ctx, "/api/v3/movie", nil, &movies); err != nil {
		return nil, err
	}
	var items []Item
	for _, m := range movies {
		if !m.HasFile || m.MovieFile == nil || m.MovieFile.Path == "" {
			continue
		}
		items = append(items, Item{Kind: Movie, Title: m.Title, TmdbID: m.TmdbID, ImdbID: m.ImdbID, Path: m.MovieFile.Path})
	}
	return items, nil
}
