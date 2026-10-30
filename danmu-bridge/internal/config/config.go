// Package config loads danmu-bridge settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	MisakaURL    string
	MisakaAPIKey string

	SonarrURL    string
	SonarrAPIKey string
	RadarrURL    string
	RadarrAPIKey string

	ListenAddr   string
	WebhookToken string

	// TVIDSource picks which id is sent to Misaka for series: "tmdb" or "tvdb".
	// The other one is used as a fallback when the preferred id is missing.
	TVIDSource string

	SyncInterval     time.Duration
	RemoveOrphans    bool
	PathMap          string
	StateDir         string
	ImportDelay      time.Duration
	TaskWaitTimeout  time.Duration
	BackfillInterval time.Duration
	HTTPTimeout      time.Duration
	LogLevel         string
}

func Load() (*Config, error) {
	c := &Config{
		MisakaURL:    strings.TrimRight(os.Getenv("MISAKA_URL"), "/"),
		MisakaAPIKey: os.Getenv("MISAKA_API_KEY"),
		SonarrURL:    strings.TrimRight(os.Getenv("SONARR_URL"), "/"),
		SonarrAPIKey: os.Getenv("SONARR_API_KEY"),
		RadarrURL:    strings.TrimRight(os.Getenv("RADARR_URL"), "/"),
		RadarrAPIKey: os.Getenv("RADARR_API_KEY"),
		ListenAddr:   env("LISTEN_ADDR", ":8090"),
		WebhookToken: env("WEBHOOK_SECRET", os.Getenv("WEBHOOK_TOKEN")),
		TVIDSource:   strings.ToLower(env("TV_ID_SOURCE", "tmdb")),
		PathMap:      os.Getenv("PATH_MAP"),
		StateDir:     env("STATE_DIR", "/data"),
		LogLevel:     env("LOG_LEVEL", "info"),
	}
	var errs []error
	durations := []struct {
		dst  *time.Duration
		key  string
		def  string
		zero bool
	}{
		{&c.SyncInterval, "SYNC_INTERVAL", "5m", false},
		{&c.ImportDelay, "IMPORT_DELAY", "0s", true},
		{&c.TaskWaitTimeout, "TASK_WAIT_TIMEOUT", "30m", true},
		{&c.BackfillInterval, "BACKFILL_INTERVAL", "20s", true},
		{&c.HTTPTimeout, "HTTP_TIMEOUT", "60s", false},
	}
	for _, d := range durations {
		v, err := parseDuration(env(d.key, d.def))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", d.key, err))
			continue
		}
		if v < 0 || (v == 0 && !d.zero) {
			errs = append(errs, fmt.Errorf("%s must be positive", d.key))
		}
		*d.dst = v
	}
	rm, err := strconv.ParseBool(env("SYNC_REMOVE_ORPHANS", "true"))
	if err != nil {
		errs = append(errs, fmt.Errorf("SYNC_REMOVE_ORPHANS: %w", err))
	}
	c.RemoveOrphans = rm

	if c.MisakaURL == "" || c.MisakaAPIKey == "" {
		errs = append(errs, errors.New("MISAKA_URL and MISAKA_API_KEY are required"))
	}
	if (c.SonarrURL == "") != (c.SonarrAPIKey == "") {
		errs = append(errs, errors.New("SONARR_URL and SONARR_API_KEY must be set together"))
	}
	if (c.RadarrURL == "") != (c.RadarrAPIKey == "") {
		errs = append(errs, errors.New("RADARR_URL and RADARR_API_KEY must be set together"))
	}
	if c.SonarrURL == "" && c.RadarrURL == "" {
		errs = append(errs, errors.New("set at least one of SONARR_URL or RADARR_URL"))
	}
	if c.TVIDSource != "tmdb" && c.TVIDSource != "tvdb" {
		errs = append(errs, errors.New("TV_ID_SOURCE must be tmdb or tvdb"))
	}
	return c, errors.Join(errs...)
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// parseDuration accepts Go durations ("90s", "5m") and bare integers as minutes.
func parseDuration(s string) (time.Duration, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Minute, nil
	}
	return time.ParseDuration(s)
}
