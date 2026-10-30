package arr

import (
	"os"
	"reflect"
	"testing"
)

func load(t *testing.T, name string) Event {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := ParseWebhook(b)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return ev
}

func TestSonarrDownload(t *testing.T) {
	ev := load(t, "sonarr_download.json")
	if ev.Source != "sonarr" || !ev.IsImport() {
		t.Fatalf("got source=%q type=%q", ev.Source, ev.Type)
	}
	want := []Item{{
		Kind: TV, Title: "葬送的芙莉莲", TmdbID: 209867, TvdbID: 424536, ImdbID: "tt22248376",
		Season: 2, Episode: 5,
		Path: "/mnt/data/library/tv/Frieren (2023)/Season 02/Frieren (2023) - S02E05 - 第5话 [WEBDL-1080p].mkv",
	}}
	if !reflect.DeepEqual(ev.Items, want) {
		t.Fatalf("items\n got %+v\nwant %+v", ev.Items, want)
	}
	if len(ev.Requests) != 1 || ev.Requests[0].Season != 2 || !reflect.DeepEqual(ev.Requests[0].Episodes, []int{5}) {
		t.Fatalf("requests %+v", ev.Requests)
	}
}

func TestSonarrImportComplete(t *testing.T) {
	ev := load(t, "sonarr_import_complete.json")
	paths := map[int]string{}
	for _, it := range ev.Items {
		paths[it.Episode] = it.Path
	}
	want := map[int]string{
		1: "/tv/Show/Season 01/Show - S01E01.mkv",
		2: "/tv/Show/Season 01/Show - s01e02.mkv", // joined from series path + relativePath
		3: "",                                     // no file in payload, still imported
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths %v", paths)
	}
	if len(ev.Requests) != 1 || EpisodeSpec(ev.Requests[0].Episodes) != "1-3" {
		t.Fatalf("requests %+v", ev.Requests)
	}
}

func TestRadarrDownload(t *testing.T) {
	ev := load(t, "radarr_download.json")
	if ev.Source != "radarr" || len(ev.Items) != 1 {
		t.Fatalf("%+v", ev)
	}
	it := ev.Items[0]
	if it.Kind != Movie || it.TmdbID != 372058 || it.ImdbID != "tt5311514" ||
		it.Path != "/mnt/data/library/movies/Your Name (2016)/Your Name (2016) [Bluray-1080p].mkv" {
		t.Fatalf("%+v", it)
	}
	if len(ev.Requests) != 1 || ev.Requests[0].Kind != Movie {
		t.Fatalf("%+v", ev.Requests)
	}
}

func TestTestEvent(t *testing.T) {
	ev := load(t, "sonarr_test.json")
	if ev.Type != "Test" || ev.IsImport() || len(ev.Requests) != 0 {
		t.Fatalf("%+v", ev)
	}
}

func TestBadPayloads(t *testing.T) {
	for _, body := range []string{`not json`, `{}`, `{"eventType":"Download"}`} {
		if _, err := ParseWebhook([]byte(body)); err == nil {
			t.Errorf("%s: want error", body)
		}
	}
	// Events we don't act on still parse.
	ev, err := ParseWebhook([]byte(`{"eventType":"Grab","series":{"title":"x"}}`))
	if err != nil || ev.IsImport() {
		t.Fatalf("%v %+v", err, ev)
	}
}

func TestEpisodeSpec(t *testing.T) {
	cases := map[string][]int{
		"":           nil,
		"1":          {1},
		"1-3,5,7-8":  {8, 1, 2, 3, 5, 7, 2},
		"1,3,5,9-11": {1, 3, 5, 9, 10, 11},
	}
	for want, in := range cases {
		if got := EpisodeSpec(in); got != want {
			t.Errorf("EpisodeSpec(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestGroupRequests(t *testing.T) {
	items := []Item{
		{Kind: TV, Title: "A", TvdbID: 1, Season: 1, Episode: 2},
		{Kind: TV, Title: "A", TvdbID: 1, Season: 1, Episode: 1},
		{Kind: TV, Title: "A", TvdbID: 1, Season: 2, Episode: 1},
		{Kind: TV, Title: "A", TvdbID: 1, Season: 0, Episode: 0}, // dropped
		{Kind: Movie, Title: "M", TmdbID: 9},
		{Kind: Movie, Title: "M", TmdbID: 9},
	}
	got := GroupRequests(items)
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	if EpisodeSpec(got[0].Episodes) != "1-2" || got[1].Season != 2 || got[2].Kind != Movie {
		t.Fatalf("%+v", got)
	}
}

func TestJoinSonarrMultiEpisodeFile(t *testing.T) {
	s := sonarrSeries{Title: "S", TvdbID: 5}
	eps := []sonarrEpisode{
		{SeasonNumber: 1, EpisodeNumber: 2, EpisodeFileID: 10, HasFile: true},
		{SeasonNumber: 1, EpisodeNumber: 1, EpisodeFileID: 10, HasFile: true},
		{SeasonNumber: 1, EpisodeNumber: 3, EpisodeFileID: 11, HasFile: true},
		{SeasonNumber: 1, EpisodeNumber: 4, HasFile: false},
	}
	files := []sonarrEpisodeFile{{ID: 10, Path: "/tv/S/S01E01-E02.mkv"}, {ID: 11, Path: "/tv/S/S01E03.mkv"}}
	got := joinSonarr(s, eps, files)
	if len(got) != 2 || got[0].Episode != 1 || got[1].Episode != 3 {
		t.Fatalf("%+v", got)
	}
}
