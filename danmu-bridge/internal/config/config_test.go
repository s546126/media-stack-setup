package config

import (
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	t.Setenv("MISAKA_URL", "http://misaka:7768/")
	t.Setenv("MISAKA_API_KEY", "k")
	t.Setenv("SONARR_URL", "http://sonarr:8989")
	t.Setenv("SONARR_API_KEY", "s")
	t.Setenv("SYNC_INTERVAL", "10")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.MisakaURL != "http://misaka:7768" || c.SyncInterval != 10*time.Minute || c.TVIDSource != "tmdb" || !c.RemoveOrphans {
		t.Fatalf("%+v", c)
	}
	t.Setenv("SYNC_INTERVAL", "90s")
	if c, _ := Load(); c.SyncInterval != 90*time.Second {
		t.Fatal(c.SyncInterval)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Setenv("MISAKA_URL", "")
	t.Setenv("RADARR_URL", "http://radarr:7878")
	t.Setenv("TV_ID_SOURCE", "bangumi")
	if _, err := Load(); err == nil {
		t.Fatal("want error")
	}
}
