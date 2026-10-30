// Package pathmap translates paths as Sonarr/Radarr see them into paths as
// this container sees them, and derives the danmaku xml path for a video.
package pathmap

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

type rule struct{ from, to string }

type Mapper struct{ rules []rule }

// Parse reads "from:to,from2:to2". An empty spec maps every path to itself.
func Parse(spec string) (*Mapper, error) {
	m := &Mapper{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		from, to, ok := strings.Cut(part, ":")
		if !ok || from == "" || to == "" {
			return nil, fmt.Errorf("bad PATH_MAP entry %q, want from:to", part)
		}
		m.rules = append(m.rules, rule{clean(from), clean(to)})
	}
	// Longest prefix wins.
	sort.SliceStable(m.rules, func(i, j int) bool { return len(m.rules[i].from) > len(m.rules[j].from) })
	return m, nil
}

func clean(p string) string {
	p = path.Clean(p)
	if p != "/" {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// Local maps an *arr path to the local filesystem path.
func (m *Mapper) Local(p string) string {
	if p == "" {
		return ""
	}
	p = path.Clean(strings.ReplaceAll(p, `\`, "/"))
	for _, r := range m.rules {
		if p == r.from {
			return r.to
		}
		prefix := r.from
		if prefix != "/" {
			prefix += "/"
		}
		if strings.HasPrefix(p, prefix) {
			return path.Join(r.to, strings.TrimPrefix(p, prefix))
		}
	}
	return p
}

// XMLPath returns the sidecar danmaku file for a video: same directory, same
// base name, ".xml" extension. The video is never resolved through symlinks,
// so the xml lands next to the symlink in the library, not in the debrid mount.
func XMLPath(video string) string {
	ext := path.Ext(video)
	if ext == "" || len(ext) > 6 {
		return video + ".xml"
	}
	return strings.TrimSuffix(video, ext) + ".xml"
}
