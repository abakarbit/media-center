package main

import (
	"embed"
	"fmt"
	"html/template"
	"math"
	"net/url"
	"sort"
	"strings"
)

//go:embed web assets
var content embed.FS

var funcs = template.FuncMap{
	"vidID": videoID,
	"thumb": thumbURL,
	"dur":   humanDur,
	"size":  humanSize,
	"inc":   func(n int) int { return n + 1 },
	"upper": strings.ToUpper,
}

var tmpl = template.Must(template.New("").Funcs(funcs).ParseFS(content, "web/*.html"))

func pad2(n int) string { return fmt.Sprintf("%02d", n) }

// urlPath mengubah rel menjadi URL /library/ yang aman per segmen.
func urlPath(rel string) string {
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return "/library/" + strings.Join(parts, "/")
}

func thumbURL(rel string) string {
	return "/thumb?id=" + url.QueryEscape(videoID(rel))
}

func humanSize(b int64) string {
	u := []string{"B", "KB", "MB", "GB", "TB"}
	n := float64(b)
	i := 0
	for n >= 1024 && i < len(u)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", int64(n), u[i])
	}
	return fmt.Sprintf("%.1f %s", n, u[i])
}

func humanDur(s float64) string {
	t := int(math.Round(s))
	h := t / 3600
	m := (t % 3600) / 60
	sec := t % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}

func videoExtList() string {
	keys := make([]string, 0, len(videoExt))
	for k := range videoExt {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// ---------- view models ----------

// headMeta mengisi tag <head> (judul, deskripsi, Open Graph/Twitter, ikon).
type headMeta struct {
	Title string
	Desc  string
	Type  string // og:type
	URL   string // URL absolut halaman (canonical / og:url)
	Image string // URL absolut; kosong = kartu tanpa gambar
}

type indexData struct {
	Head       headMeta
	Hero       *Video
	Playlists  []Playlist
	Root       []*Video
	Total      int
	HasFolders bool
	Ext        string
}

type playlistData struct {
	Head  headMeta
	Dir   string
	Label string
	PlID  string
	Items []*Video
	First *Video
	Total float64
}

type watchData struct {
	Head    headMeta
	V       *Video
	Src     string
	SubURL  string
	PlLabel string
	PlID    string
	Idx     int // 1-based
	Count   int
	Prev    *Video
	Next    *Video
	Others  []*Video
	NextURL string
}
