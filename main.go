package main

import (
	"fmt"
	"html"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ---------- config ----------

type config struct {
	LibDir      string
	CacheDir    string
	ThumbDir    string
	Addr        string
	Concurrency int
	RescanToken string
}

var cfg config

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func mustDirs(dirs ...string) {
	for _, d := range dirs {
		_ = os.MkdirAll(d, 0o775)
	}
}

// mustReal membuat direktori bila perlu lalu mengembalikan path kanonik (symlink resolved),
// agar pengecekan containment tidak salah tolak.
func mustReal(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	_ = os.MkdirAll(abs, 0o775)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return filepath.Clean(abs)
}

// ---------- rendering ----------

func renderPage(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

// pageBase mengembalikan skema+host absolut untuk canonical/Open Graph.
func pageBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	return scheme + "://" + r.Host
}

func notFound(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, `<!doctype html><html lang="id"><head><meta charset="utf-8">`+
		`<meta name="viewport" content="width=device-width, initial-scale=1">`+
		`<title>404 — Media Center</title>`+
		`<link rel="icon" href="/assets/icon.svg" type="image/svg+xml">`+
		`<link rel="stylesheet" href="/assets/style.css"></head>`+
		`<body class="err-page"><div class="err-box"><h2>%s</h2>`+
		`<p><a href="/">&larr; Beranda</a></p></div></body></html>`,
		html.EscapeString(msg))
}

// ---------- handlers ----------

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		notFound(w, "Halaman tidak ditemukan")
		return
	}
	g := groupedLibrary()
	hero := newestVideo(g.All)
	base := pageBase(r)
	img := base + "/assets/icon-512.png"
	if hero != nil {
		img = base + thumbURL(hero.Rel)
	}
	renderPage(w, http.StatusOK, "index.html", indexData{
		Head: headMeta{
			Title: "Media Center",
			Desc:  "Media Center — streaming film & serial dari folder library. Playlist otomatis, subtitle, lanjut tonton dari posisi terakhir.",
			Type:  "website",
			URL:   base + r.URL.RequestURI(),
			Image: img,
		},
		Hero:       hero,
		Playlists:  g.Playlists,
		Root:       g.Root,
		Total:      len(g.All),
		HasFolders: len(g.Playlists) > 0,
		Ext:        videoExtList(),
	})
}

func playlistHandler(w http.ResponseWriter, r *http.Request) {
	dir, ok := idToRel(r.URL.Query().Get("pl"))
	if !ok || dir == "" {
		notFound(w, "Playlist tidak ditemukan")
		return
	}
	g := groupedLibrary()
	var pl *Playlist
	for i := range g.Playlists {
		if g.Playlists[i].Dir == dir {
			pl = &g.Playlists[i]
			break
		}
	}
	if pl == nil {
		notFound(w, "Playlist tidak ditemukan")
		return
	}
	var total float64
	for _, it := range pl.Items {
		total += it.Duration
	}
	data := playlistData{Dir: pl.Dir, Label: pl.Label, PlID: videoID(pl.Dir), Items: pl.Items, Total: total}
	base := pageBase(r)
	img := base + "/assets/icon-512.png"
	if len(pl.Items) > 0 {
		data.First = pl.Items[0]
		img = base + thumbURL(data.First.Rel)
	}
	data.Head = headMeta{
		Title: pl.Label + " — Media Center",
		Desc:  fmt.Sprintf("Playlist %s — %d episode di Media Center.", pl.Label, len(pl.Items)),
		Type:  "website",
		URL:   base + r.URL.RequestURI(),
		Image: img,
	}
	renderPage(w, http.StatusOK, "playlist.html", data)
}

func watchHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	rel, ok := idToRel(id)
	if !ok {
		notFound(w, "Video tidak ditemukan")
		return
	}
	abs, ok := safePath(rel)
	if !ok {
		notFound(w, "Video tidak ditemukan")
		return
	}
	if ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(abs), ".")); !videoExt[ext] {
		notFound(w, "Video tidak ditemukan")
		return
	}

	g := groupedLibrary()
	var v *Video
	for _, x := range g.All {
		if x.Rel == rel {
			v = x
			break
		}
	}
	if v == nil {
		notFound(w, "Video tidak ditemukan")
		return
	}

	var siblings []*Video
	plLabel, plID := "", ""
	if v.Pl != "" {
		for _, p := range g.Playlists {
			if p.Dir == v.Pl {
				siblings = p.Items
				plLabel = p.Label
				plID = videoID(p.Dir)
				break
			}
		}
	}
	if siblings == nil {
		siblings = g.Root
	}

	idx := -1
	for n, s := range siblings {
		if s.Rel == rel {
			idx = n
			break
		}
	}
	var prev, next *Video
	if idx > 0 {
		prev = siblings[idx-1]
	}
	if idx >= 0 && idx < len(siblings)-1 {
		next = siblings[idx+1]
	}
	others := make([]*Video, 0, len(siblings))
	for _, s := range siblings {
		if s.Rel != rel {
			others = append(others, s)
		}
	}

	subURL := ""
	if subRel, ext, ok := subtitleFor(rel); ok {
		if ext == "vtt" {
			subURL = urlPath(subRel)
		} else {
			subURL = "/subs?id=" + url.QueryEscape(id)
		}
	}

	nextURL := ""
	if next != nil {
		nextURL = "/watch?id=" + url.QueryEscape(videoID(next.Rel))
		if plID != "" {
			nextURL += "&pl=" + url.QueryEscape(plID)
		}
	}

	renderPage(w, http.StatusOK, "watch.html", watchData{
		Head: headMeta{
			Title: v.Title + " — Media Center",
			Desc:  fmt.Sprintf("Tonton %s (%s) di Media Center.", v.Title, humanDur(v.Duration)),
			Type:  "video.other",
			URL:   pageBase(r) + r.URL.RequestURI(),
			Image: pageBase(r) + thumbURL(v.Rel),
		},
		V: v, Src: urlPath(rel), SubURL: subURL,
		PlLabel: plLabel, PlID: plID, Idx: idx + 1, Count: len(siblings),
		Prev: prev, Next: next, Others: others, NextURL: nextURL,
	})
}

var mimeFallback = "application/octet-stream"

func libraryHandler(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/library/")
	if rel == "" {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(rel), "."))
	if !serveExt[ext] {
		http.Error(w, "403", http.StatusForbidden)
		return
	}
	abs, ok := safePath(rel)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if ct, ok := mimeByExt[ext]; ok {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", mimeFallback)
	}
	http.ServeFile(w, r, abs)
}

var reSVGLabel = regexp.MustCompile(`[^\p{L}\p{N}]`) // hanya huruf/angka untuk label fallback

func thumbHandler(w http.ResponseWriter, r *http.Request) {
	rel, ok := idToRel(r.URL.Query().Get("id"))
	if !ok {
		http.Error(w, "400", http.StatusBadRequest)
		return
	}
	if ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(rel), ".")); !videoExt[ext] {
		http.NotFound(w, r)
		return
	}
	if _, ok := safePath(rel); !ok {
		http.NotFound(w, r)
		return
	}

	if p, ok := thumbFor(r.Context(), rel, true); ok {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, p)
		return
	}

	label := svgLabel(rel)
	w.Header().Set("Content-Type", "image/svg+xml; charset=UTF-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="480" height="270" viewBox="0 0 480 270">`+
		`<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">`+
		`<stop offset="0" stop-color="#1b1b26"/><stop offset="1" stop-color="#0e0e15"/>`+
		`</linearGradient></defs><rect width="480" height="270" fill="url(#g)"/>`+
		`<text x="240" y="150" font-family="system-ui,Arial" font-size="72" font-weight="700" `+
		`fill="#3a3a4d" text-anchor="middle">%s</text></svg>`, html.EscapeString(label))
}

func svgLabel(rel string) string {
	if ep := parseEpisode(rel); ep != nil {
		return "E" + pad2(*ep)
	}
	t := displayTitle(rel)
	var b strings.Builder
	for _, r := range strings.ToUpper(t) {
		if reSVGLabel.MatchString(string(r)) {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 2 {
			break
		}
	}
	if b.Len() == 0 {
		return "MC"
	}
	return b.String()
}

var (
	reSRTNum = regexp.MustCompile(`(?m)^\d+\s*$`)
	reSRTTS  = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)
)

func subsHandler(w http.ResponseWriter, r *http.Request) {
	rel, ok := idToRel(r.URL.Query().Get("id"))
	if !ok {
		http.Error(w, "400", http.StatusBadRequest)
		return
	}
	subRel, ext, ok := subtitleFor(rel)
	if !ok {
		http.NotFound(w, r)
		return
	}
	abs, ok := safePath(subRel)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	if ext == "vtt" {
		http.ServeFile(w, r, abs)
		return
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	txt := strings.ReplaceAll(string(raw), "\r\n", "\n")
	txt = strings.ReplaceAll(txt, "\r", "\n")
	txt = strings.TrimPrefix(txt, "\uFEFF")
	txt = reSRTNum.ReplaceAllString(txt, "")
	txt = reSRTTS.ReplaceAllString(txt, "$1.$2")
	_, _ = w.Write([]byte("WEBVTT\n\n" + txt))
}

func rescanHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "405 method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "403", http.StatusForbidden)
		return
	}
	if cfg.RescanToken != "" && r.URL.Query().Get("token") != cfg.RescanToken {
		http.Error(w, "403", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	g := groupedLibrary()
	n := 0
	for _, v := range g.All {
		if _, ok := thumbFor(r.Context(), v.Rel, true); ok {
			n++
		}
	}
	fmt.Fprintf(w, "%d video · %d poster\n", len(g.All), n)
}

func assetsHandler() http.Handler {
	sub, err := fs.Sub(content, "assets")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/assets/", http.FileServer(http.FS(sub)))
}

// ---------- main ----------

func main() {
	cfg = config{
		LibDir:      env("LIB_DIR", "./library"),
		CacheDir:    env("CACHE_DIR", "./.cache"),
		ThumbDir:    env("THUMB_DIR", "./.thumbs"),
		Addr:        ":" + env("PORT", "8080"),
		Concurrency: envInt("FF_CONCURRENCY", runtime.NumCPU()),
		RescanToken: os.Getenv("RESCAN_TOKEN"),
	}
	mustDirs(cfg.LibDir, cfg.CacheDir, cfg.ThumbDir)
	cfg.LibDir = mustReal(cfg.LibDir)
	cfg.CacheDir = mustReal(cfg.CacheDir)
	cfg.ThumbDir = mustReal(cfg.ThumbDir)
	lib.path = filepath.Join(cfg.CacheDir, "library.json")

	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	ffSem = make(chan struct{}, cfg.Concurrency)

	mux := http.NewServeMux()
	mux.Handle("/assets/", assetsHandler())
	mux.HandleFunc("/library/", libraryHandler)
	mux.HandleFunc("/thumb", thumbHandler)
	mux.HandleFunc("/subs", subsHandler)
	mux.HandleFunc("/rescan", rescanHandler)
	mux.HandleFunc("/playlist", playlistHandler)
	mux.HandleFunc("/watch", watchHandler)
	mux.HandleFunc("/", indexHandler)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           withSecurity(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout sengaja 0: streaming video panjang tak boleh diputus.
	}
	log.Printf("mediacenter %s · lib=%s · ff_concurrency=%d", cfg.Addr, cfg.LibDir, cfg.Concurrency)
	log.Fatal(srv.ListenAndServe())
}
