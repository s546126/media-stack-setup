package pathmap

import "testing"

func TestLocal(t *testing.T) {
	m, err := Parse("/data/media:/mnt/data/library, /data/media/movies:/movies ,/tv:/srv/tv/")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"/data/media/tv/A/S01E01.mkv":   "/mnt/data/library/tv/A/S01E01.mkv",
		"/data/media/movies/B/B.mkv":    "/movies/B/B.mkv", // longest prefix wins
		"/data/mediaX/C.mkv":            "/data/mediaX/C.mkv",
		"/tv/Show/Season 1/E1.mkv":      "/srv/tv/Show/Season 1/E1.mkv",
		"/tv":                           "/srv/tv",
		"/other/x.mkv":                  "/other/x.mkv",
		"/data/media/tv/../tv/A/E1.mkv": "/mnt/data/library/tv/A/E1.mkv",
		"":                              "",
	}
	for in, want := range cases {
		if got := m.Local(in); got != want {
			t.Errorf("Local(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIdentity(t *testing.T) {
	m, err := Parse("")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Local("/mnt/data/library/tv/a.mkv"); got != "/mnt/data/library/tv/a.mkv" {
		t.Fatal(got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{"nocolon", ":/x", "/x:"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q): want error", s)
		}
	}
}

func TestXMLPath(t *testing.T) {
	cases := map[string]string{
		"/lib/tv/Show/Season 01/Show - S01E01 - 第1话 [WEBDL-1080p].mkv": "/lib/tv/Show/Season 01/Show - S01E01 - 第1话 [WEBDL-1080p].xml",
		"/lib/movies/Your Name (2016)/Your.Name.2016.mp4":              "/lib/movies/Your Name (2016)/Your.Name.2016.xml",
		"/lib/a/noext":                       "/lib/a/noext.xml",
		"/lib/a/Movie.Title.2016.Remastered": "/lib/a/Movie.Title.2016.Remastered.xml",
	}
	for in, want := range cases {
		if got := XMLPath(in); got != want {
			t.Errorf("XMLPath(%q) = %q, want %q", in, got, want)
		}
	}
}
