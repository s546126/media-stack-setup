// Command danmu-bridge connects Sonarr/Radarr to misaka_danmu_server.
//
//	danmu-bridge [serve]        webhook server + periodic xml sync (default)
//	danmu-bridge backfill ...   submit every existing file to Misaka
//	danmu-bridge sync           run one xml sync and exit
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/s546126/danmu-bridge/internal/arr"
	"github.com/s546126/danmu-bridge/internal/bridge"
	"github.com/s546126/danmu-bridge/internal/config"
	"github.com/s546126/danmu-bridge/internal/misaka"
	"github.com/s546126/danmu-bridge/internal/pathmap"
)

var version = "dev"

func main() {
	cmd, args := "serve", os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	if cmd == "version" {
		fmt.Println(version)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	log := newLogger(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := wire(cfg, log)
	if err != nil {
		log.Error("setup", "err", err)
		os.Exit(2)
	}

	switch cmd {
	case "serve":
		err = serve(ctx, cfg, app, log)
	case "sync":
		var st bridge.SyncStats
		st, err = app.syncer.RunOnce(ctx)
		log.Info("sync done", "stats", fmt.Sprintf("%+v", st))
	case "backfill":
		err = backfill(ctx, cfg, app, log, args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (serve, backfill, sync, version)\n", cmd)
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error(cmd+" failed", "err", err)
		os.Exit(1)
	}
}

type app struct {
	importer *bridge.Importer
	syncer   *bridge.Syncer
	listers  []bridge.Lister
}

func wire(cfg *config.Config, log *slog.Logger) (*app, error) {
	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return nil, err
	}
	pm, err := pathmap.Parse(cfg.PathMap)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{Timeout: cfg.HTTPTimeout}
	m := &misaka.Client{BaseURL: cfg.MisakaURL, APIKey: cfg.MisakaAPIKey, HTTP: hc}

	var listers []bridge.Lister
	if cfg.SonarrURL != "" {
		c := &arr.Client{BaseURL: cfg.SonarrURL, APIKey: cfg.SonarrAPIKey, HTTP: hc}
		listers = append(listers, c.SonarrItems)
	}
	if cfg.RadarrURL != "" {
		c := &arr.Client{BaseURL: cfg.RadarrURL, APIKey: cfg.RadarrAPIKey, HTTP: hc}
		listers = append(listers, c.RadarrItems)
	}
	return &app{
		importer: &bridge.Importer{Misaka: m, TVIDSource: cfg.TVIDSource, TaskWait: cfg.TaskWaitTimeout, Log: log},
		syncer: &bridge.Syncer{
			Misaka: m, Listers: listers, Paths: pm, RemoveOrphans: cfg.RemoveOrphans, Log: log,
			StatePath: filepath.Join(cfg.StateDir, "sync-state.json"),
		},
		listers: listers,
	}, nil
}

func serve(ctx context.Context, cfg *config.Config, a *app, log *slog.Logger) error {
	if cfg.WebhookToken == "" {
		log.Warn("WEBHOOK_SECRET is empty: anyone who can reach LISTEN_ADDR can queue imports")
	}
	srv := &bridge.Server{
		Importer: a.importer, Syncer: a.syncer, Token: cfg.WebhookToken,
		ImportDelay: cfg.ImportDelay, SyncInterval: cfg.SyncInterval, Log: log,
	}
	hs := &http.Server{Addr: cfg.ListenAddr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.ListenAddr, "syncInterval", cfg.SyncInterval)
		errc <- hs.ListenAndServe()
	}()
	go srv.Run(ctx)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return hs.Shutdown(sctx)
	}
}

func backfill(ctx context.Context, cfg *config.Config, a *app, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("backfill", flag.ExitOnError)
	only := fs.String("only", "", "tv or movie (default: both)")
	limit := fs.Int("limit", 0, "submit at most N requests this run")
	dry := fs.Bool("dry-run", false, "list what would be submitted")
	reset := fs.Bool("reset", false, "forget previous progress and start over")
	interval := fs.Duration("interval", cfg.BackfillInterval, "pause between submissions")
	_ = fs.Parse(args)
	if *only != "" && *only != "tv" && *only != "movie" {
		return fmt.Errorf("-only must be tv or movie")
	}
	statePath := filepath.Join(cfg.StateDir, "backfill-state.json")
	if *reset {
		if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	b := &bridge.Backfill{
		Importer: a.importer, Listers: a.listers, StatePath: statePath,
		Interval: *interval, Only: arr.Kind(*only), Limit: *limit, DryRun: *dry, Log: log,
	}
	st, err := b.Run(ctx)
	log.Info("backfill finished", "total", st.Total, "alreadyDone", st.Skipped,
		"submitted", st.Submitted, "duplicate", st.Duplicate, "failed", st.Failed)
	return err
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}
