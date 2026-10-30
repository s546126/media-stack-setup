// Package misaka is a client for misaka_danmu_server's external control API
// (/api/control, authenticated by the "external API key" from its settings).
package misaka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Client struct {
	BaseURL string // e.g. http://misaka:7768
	APIKey  string
	HTTP    *http.Client
}

// APIError is a non-2xx answer. Detail is FastAPI's "detail" field when present.
type APIError struct {
	Status int
	Detail string
}

func (e *APIError) Error() string { return fmt.Sprintf("misaka: HTTP %d: %s", e.Status, e.Detail) }

// StatusOf returns the HTTP status of an APIError, or 0.
func StatusOf(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

func (c *Client) do(ctx context.Context, method, p string, q url.Values, out any) error {
	u := c.BaseURL + "/api/control" + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	// Header rather than ?api_key= so the key stays out of access logs.
	req.Header.Set("X-API-Key", c.APIKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return &APIError{Status: resp.StatusCode, Detail: detail(b)}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func detail(b []byte) string {
	var v struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(b, &v) == nil && len(v.Detail) > 0 {
		var s string
		if json.Unmarshal(v.Detail, &s) == nil {
			return s
		}
		return string(v.Detail)
	}
	return strings.TrimSpace(string(b))
}

type AutoImport struct {
	SearchType string // tmdb, tvdb, imdb, ...
	SearchTerm string
	MediaType  string // tv_series or movie
	Season     int    // 0 = omit
	Episodes   string // "1,3,5-7"; empty = whole season / movie
}

// AutoImport submits POST /import/auto and returns the task id.
// Misaka answers 429 while another search runs for the same key, and 409
// for a duplicate task or a season already in its library.
func (c *Client) AutoImport(ctx context.Context, r AutoImport) (string, error) {
	q := url.Values{"searchType": {r.SearchType}, "searchTerm": {r.SearchTerm}}
	if r.MediaType != "" {
		q.Set("mediaType", r.MediaType)
	}
	if r.Season > 0 {
		q.Set("season", strconv.Itoa(r.Season))
	}
	if r.Episodes != "" {
		q.Set("episode", r.Episodes)
	}
	var out struct {
		TaskID string `json:"taskId"`
	}
	if err := c.do(ctx, http.MethodPost, "/import/auto", q, &out); err != nil {
		return "", err
	}
	return out.TaskID, nil
}

type Task struct {
	TaskID      string `json:"taskId"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Progress    int    `json:"progress"`
	Description string `json:"description"`
}

// Task status strings as Misaka's TaskManager reports them.
const (
	StatusPending   = "排队中"
	StatusRunning   = "运行中"
	StatusCompleted = "已完成"
	StatusFailed    = "失败"
	StatusPaused    = "已暂停"
	StatusCancelled = "已取消"
	StatusTimeout   = "超时"
)

func (t Task) Done() bool {
	switch t.Status {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusTimeout:
		return true
	}
	return false
}

func (c *Client) Task(ctx context.Context, id string) (Task, error) {
	var t Task
	err := c.do(ctx, http.MethodGet, "/tasks/"+url.PathEscape(id), nil, &t)
	return t, err
}

type SourceBrief struct {
	SourceID     int    `json:"sourceId"`
	ProviderName string `json:"providerName"`
	IsFavorited  bool   `json:"isFavorited"`
}

type LibraryAnime struct {
	AnimeID int           `json:"animeId"`
	Title   string        `json:"title"`
	Type    string        `json:"type"`
	Season  int           `json:"season"`
	Sources []SourceBrief `json:"sources"`
}

func (c *Client) Library(ctx context.Context) ([]LibraryAnime, error) {
	var out []LibraryAnime
	err := c.do(ctx, http.MethodGet, "/library", nil, &out)
	return out, err
}

type AnimeDetails struct {
	Title  string `json:"title"`
	Type   string `json:"type"`
	Season int    `json:"season"`
	TmdbID string `json:"tmdbId"`
	TvdbID string `json:"tvdbId"`
	ImdbID string `json:"imdbId"`
}

func (c *Client) Anime(ctx context.Context, id int) (AnimeDetails, error) {
	var out AnimeDetails
	err := c.do(ctx, http.MethodGet, "/library/anime/"+strconv.Itoa(id), nil, &out)
	return out, err
}

type Episode struct {
	EpisodeID    int    `json:"episodeId"`
	Title        string `json:"title"`
	EpisodeIndex int    `json:"episodeIndex"`
	FetchedAt    string `json:"fetchedAt"`
	CommentCount int    `json:"commentCount"`
}

func (c *Client) SourceEpisodes(ctx context.Context, sourceID int) ([]Episode, error) {
	var out []Episode
	err := c.do(ctx, http.MethodGet, "/library/source/"+strconv.Itoa(sourceID)+"/episodes", nil, &out)
	return out, err
}

type Comment struct {
	CID int64  `json:"cid"`
	P   string `json:"p"`
	M   string `json:"m"`
}

// Comments returns every stored comment of an episode, unfiltered by
// Misaka's output limits.
func (c *Client) Comments(ctx context.Context, episodeID int) ([]Comment, error) {
	var out struct {
		Comments []Comment `json:"comments"`
	}
	err := c.do(ctx, http.MethodGet, "/danmaku/"+strconv.Itoa(episodeID), nil, &out)
	return out.Comments, err
}
