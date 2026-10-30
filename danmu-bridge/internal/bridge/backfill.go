package bridge

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/s546126/danmu-bridge/internal/arr"
	"github.com/s546126/danmu-bridge/internal/state"
)

type BackfillState struct {
	// Done maps request key to the episode spec that was submitted. A season
	// gets resubmitted when new episode files appear in it.
	Done map[string]string `json:"done"`
}

type Backfill struct {
	Importer  *Importer
	Listers   []Lister
	StatePath string
	Interval  time.Duration
	Only      arr.Kind // "" = both
	Limit     int      // 0 = no limit
	DryRun    bool
	Log       *slog.Logger
}

type BackfillStats struct{ Total, Skipped, Submitted, Duplicate, Failed int }

func (b *Backfill) Run(ctx context.Context) (BackfillStats, error) {
	var st BackfillStats
	var items []arr.Item
	for _, l := range b.Listers {
		its, err := l(ctx)
		if err != nil {
			return st, err
		}
		items = append(items, its...)
	}
	var reqs []arr.ImportRequest
	for _, r := range arr.GroupRequests(items) {
		if b.Only == "" || r.Kind == b.Only {
			reqs = append(reqs, r)
		}
	}
	st.Total = len(reqs)

	bs := BackfillState{Done: map[string]string{}}
	if err := state.Load(b.StatePath, &bs); err != nil {
		return st, err
	}
	if bs.Done == nil {
		bs.Done = map[string]string{}
	}

	sent := 0
	for i, r := range reqs {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		spec := arr.EpisodeSpec(r.Episodes)
		if bs.Done[r.Key()] == spec {
			st.Skipped++
			continue
		}
		if b.Limit > 0 && sent >= b.Limit {
			break
		}
		if b.DryRun {
			req, err := Request(r, b.Importer.TVIDSource)
			b.Log.Info("dry-run", "n", i+1, "of", len(reqs), "item", r.String(),
				"searchType", req.SearchType, "searchTerm", req.SearchTerm, "err", err)
			sent++
			continue
		}
		if sent > 0 && b.Interval > 0 {
			if err := sleep(ctx, b.Interval); err != nil {
				return st, err
			}
		}
		sent++
		out, err := b.Importer.Run(ctx, r)
		switch {
		case errors.Is(err, context.Canceled):
			return st, err
		case err != nil:
			st.Failed++
			b.Log.Error("backfill item failed", "n", i+1, "of", len(reqs), "item", r.String(), "err", err)
			continue
		case out == Duplicate:
			st.Duplicate++
		default:
			st.Submitted++
		}
		bs.Done[r.Key()] = spec
		if err := state.Save(b.StatePath, &bs); err != nil {
			return st, err
		}
		b.Log.Info("backfill progress", "n", i+1, "of", len(reqs), "item", r.String())
	}
	return st, nil
}
