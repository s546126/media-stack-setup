// Package danmaku renders Misaka comments as a bilibili-style danmaku xml.
package danmaku

import (
	"bytes"
	"encoding/xml"
	"sort"
	"strconv"
	"strings"

	"github.com/s546126/danmu-bridge/internal/misaka"
)

// RenderXML writes the same layout Misaka's own xml_generator produces:
// an <i> root with chat headers and one <d p="..."> per comment, sorted by
// time so the output (and its hash) is stable between runs.
func RenderXML(episodeID int, comments []misaka.Comment) []byte {
	cs := append([]misaka.Comment(nil), comments...)
	sort.SliceStable(cs, func(i, j int) bool {
		ti, tj := startTime(cs[i].P), startTime(cs[j].P)
		if ti != tj {
			return ti < tj
		}
		return cs[i].CID < cs[j].CID
	})

	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString("<i>\n")
	tag(&b, "chatserver", "danmaku.misaka.org")
	tag(&b, "chatid", strconv.Itoa(episodeID))
	tag(&b, "mission", "0")
	tag(&b, "maxlimit", "2000")
	tag(&b, "source", "k-v")
	tag(&b, "sourceprovider", "misaka")
	tag(&b, "datasize", strconv.Itoa(len(cs)))
	for _, c := range cs {
		b.WriteString(`  <d p="`)
		escape(&b, c.P)
		b.WriteString(`">`)
		escape(&b, c.M)
		b.WriteString("</d>\n")
	}
	b.WriteString("</i>\n")
	return b.Bytes()
}

func tag(b *bytes.Buffer, name, val string) {
	b.WriteString("  <" + name + ">")
	escape(b, val)
	b.WriteString("</" + name + ">\n")
}

// escape drops characters that are illegal in XML 1.0 (control codes show up
// in scraped danmaku) and escapes the rest.
func escape(b *bytes.Buffer, s string) {
	clean := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r <= 0xD7FF) || (r >= 0xE000 && r <= 0xFFFD) || (r >= 0x10000 && r <= 0x10FFFF) {
			return r
		}
		return -1
	}, s)
	_ = xml.EscapeText(b, []byte(clean))
}

func startTime(p string) float64 {
	first, _, _ := strings.Cut(p, ",")
	f, err := strconv.ParseFloat(first, 64)
	if err != nil {
		return 0
	}
	return f
}
