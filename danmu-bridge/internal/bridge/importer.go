// Package bridge wires Sonarr/Radarr to Misaka: submitting imports, mirroring
// danmaku xml next to videos, and the webhook server.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/s546126/danmu-bridge/internal/arr"
	"github.com/s546126/danmu-bridge/internal/misaka"
)

// Outcome of one import request.
type Outcome int

const (
	Submitted Outcome = iota // task submitted (and finished, if waiting)
	Duplicate                // Misaka said 409: already queued recently or already in library
)

// ErrPermanent wraps errors that retrying won't fix (bad id, metadata source disabled, ...).
var ErrPermanent = errors.New("permanent")

type Importer struct {
	Misaka     *misaka.Client
	TVIDSource string        // "tmdb" or "tvdb"
	TaskWait   time.Duration // 0 = don't wait for the Misaka task
	Log        *slog.Logger

	PollEvery  time.Duration // default 10s
	RetryBase  time.Duration // default 15s, doubles up to RetryMax
	RetryMax   time.Duration // default 5m
	MaxRetries int           // default 12
}

// Request builds the Misaka auto-import call for an *arr request.
func Request(r arr.ImportRequest, tvIDSource string) (misaka.AutoImport, error) {
	if r.Kind == arr.Movie {
		switch {
		case r.TmdbID > 0:
			return misaka.AutoImport{SearchType: "tmdb", SearchTerm: strconv.Itoa(r.TmdbID), MediaType: "movie"}, nil
		case r.ImdbID != "":
			return misaka.AutoImport{SearchType: "imdb", SearchTerm: r.ImdbID, MediaType: "movie"}, nil
		}
		return misaka.AutoImport{}, fmt.Errorf("%w: movie %q has no tmdb/imdb id", ErrPermanent, r.Title)
	}
	ids := []struct {
		typ string
		id  int
	}{{"tmdb", r.TmdbID}, {"tvdb", r.TvdbID}}
	if tvIDSource == "tvdb" {
		ids[0], ids[1] = ids[1], ids[0]
	}
	for _, c := range ids {
		if c.id > 0 {
			return misaka.AutoImport{
				SearchType: c.typ, SearchTerm: strconv.Itoa(c.id), MediaType: "tv_series",
				Season: r.Season, Episodes: arr.EpisodeSpec(r.Episodes),
			}, nil
		}
	}
	return misaka.AutoImport{}, fmt.Errorf("%w: series %q has no tmdb/tvdb id", ErrPermanent, r.Title)
}

// Run submits one request, retrying while Misaka is busy, then optionally
// waits for the task to finish. Misaka allows one running search per API key,
// so callers must run imports one at a time.
func (im *Importer) Run(ctx context.Context, r arr.ImportRequest) (Outcome, error) {
	req, err := Request(r, im.TVIDSource)
	if err != nil {
		return 0, err
	}
	backoff := or(im.RetryBase, 15*time.Second)
	maxBackoff := or(im.RetryMax, 5*time.Minute)
	maxRetries := im.MaxRetries
	if maxRetries == 0 {
		maxRetries = 12
	}

	var taskID string
	for attempt := 0; ; attempt++ {
		taskID, err = im.Misaka.AutoImport(ctx, req)
		if err == nil {
			break
		}
		st := misaka.StatusOf(err)
		switch {
		case st == http.StatusConflict:
			im.Log.Info("misaka: already queued or in library", "item", r.String(), "detail", err)
			return Duplicate, nil
		case st == http.StatusTooManyRequests, st >= 500, st == 0 && ctx.Err() == nil:
			// Busy, server error or network error: retry.
		default:
			return 0, fmt.Errorf("%w: %v", ErrPermanent, err)
		}
		if attempt >= maxRetries {
			return 0, fmt.Errorf("giving up after %d retries: %w", attempt, err)
		}
		im.Log.Debug("misaka busy, retrying", "item", r.String(), "in", backoff, "err", err)
		if err := sleep(ctx, backoff); err != nil {
			return 0, err
		}
		backoff = min(backoff*2, maxBackoff)
	}
	im.Log.Info("misaka: import submitted", "item", r.String(), "task", taskID,
		"searchType", req.SearchType, "searchTerm", req.SearchTerm)

	if im.TaskWait <= 0 || taskID == "" {
		return Submitted, nil
	}
	t, err := im.wait(ctx, taskID)
	if err != nil {
		return Submitted, err
	}
	if t.Status != misaka.StatusCompleted {
		return Submitted, fmt.Errorf("misaka task %s ended %s: %s", taskID, t.Status, t.Description)
	}
	im.Log.Info("misaka: import finished", "item", r.String(), "task", taskID, "result", t.Description)
	return Submitted, nil
}

func (im *Importer) wait(ctx context.Context, id string) (misaka.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, im.TaskWait)
	defer cancel()
	every := or(im.PollEvery, 10*time.Second)
	for {
		if err := sleep(ctx, every); err != nil {
			return misaka.Task{}, fmt.Errorf("waiting for misaka task %s: %w", id, err)
		}
		t, err := im.Misaka.Task(ctx, id)
		if err != nil {
			if misaka.StatusOf(err) == http.StatusNotFound {
				continue // not in history yet
			}
			im.Log.Debug("poll task", "task", id, "err", err)
			continue
		}
		if t.Done() {
			return t, nil
		}
	}
}

func or(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
