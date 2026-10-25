package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	videoExt = map[string]bool{"mp4": true, "mkv": true, "webm": true, "m4v": true, "mov": true, "avi": true}
	serveExt = map[string]bool{
		"mp4": true, "mkv": true, "webm": true, "m4v": true, "mov": true, "avi": true,
		"vtt": true, "srt": true,
	}
	mimeByExt = map[string]string{
		"mp4": "video/mp4", "m4v": "video/mp4", "webm": "video/webm",
		"mov": "video/quicktime", "mkv": "video/x-matroska", "avi": "video/x-msvideo",
		"vtt": "text/vtt; charset=utf-8", "srt": "text/plain; charset=utf-8",
	}
)

// Video adalah satu berkas video + metadata ffprobe.
// Tag JSON cocok dengan skema .cache/library.json yang sudah ada, sehingga
// cache lama langsung terbaca tanpa pemindaian ulang.
type Video struct {
	Rel       string  `json:"rel"`
	Title     string  `json:"title"`
	TitleSort string  `json:"title_sort"`
	Size      int64   `json:"size"`
	Mtime     int64   `json:"mtime"`
	Sig       string  `json:"sig"`
	Duration  float64 `json:"duration"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Vcodec    string  `json:"vcodec"`
	Acodec    string  `json:"acodec"`

	Ep *int   `json:"-"` // nomor episode (E01, ...), nil kalau tak ada
	Pl string `json:"-"` // playlist = folder relatif, "" = langsung di library/
}

func (v *Video) EpLabel() string {
	if v.Ep == nil {
		return ""
	}
	return "E" + pad2(*v.Ep)
}

func (v *Video) EpNum() int {
	if v.Ep == nil {
		return 0
	}
	return *v.Ep
}

type cachedLib struct {
	mu   sync.Mutex
	path string
}

var lib cachedLib

func cachePath() string { return lib.path }

// ---------- penemuan berkas ----------

func findVideos() []string {
	var out []string
	_ = filepath.WalkDir(cfg.LibDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if p == cfg.LibDir {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 { // jangan ikuti symlink
			return nil
		}
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "._") {
			return nil
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		if !videoExt[ext] {
			return nil
		}
		out = append(out, p)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return naturalLess(relPath(out[i]), relPath(out[j])) })
	return out
}

func relPath(abs string) string {
	r, err := filepath.Rel(cfg.LibDir, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(r)
}

// ---------- metadata ----------

type probeMeta struct {
	Duration float64
	Width    int
	Height   int
	Vcodec   string
	Acodec   string
}

// ffprobeMeta menjalankan ffprobe tanpa shell; abs selalu path absolut
// sehingga nama berkas tidak bisa disalahartikan sebagai flag.
func ffprobeMeta(ctx context.Context, abs string) probeMeta {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", abs)
	out, err := cmd.Output()
	if err != nil {
		return probeMeta{}
	}
	var j struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}
	if json.Unmarshal(out, &j) != nil {
		return probeMeta{}
	}
	var m probeMeta
	m.Duration, _ = strconv.ParseFloat(j.Format.Duration, 64)
	for _, s := range j.Streams {
		switch s.CodecType {
		case "video":
			if m.Width == 0 {
				m.Width, m.Height, m.Vcodec = s.Width, s.Height, s.CodecName
			}
		case "audio":
			if m.Acodec == "" {
				m.Acodec = s.CodecName
			}
		}
	}
	return m
}

type scanJob struct {
	abs   string
	rel   string
	size  int64
	mtime int64
}

func sigOf(size, mtime int64) string {
	return strconv.FormatInt(size, 10) + ":" + strconv.FormatInt(mtime, 10)
}

func probeAll(jobs []scanJob) []*Video {
	out := make([]*Video, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		acquireFF()
		go func(i int, j scanJob) {
			defer wg.Done()
			defer releaseFF()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			md := ffprobeMeta(ctx, j.abs)
			title := displayTitle(j.rel)
			out[i] = &Video{
				Rel: j.rel, Title: title, TitleSort: title,
				Size: j.size, Mtime: j.mtime, Sig: sigOf(j.size, j.mtime),
				Duration: md.Duration, Width: md.Width, Height: md.Height,
				Vcodec: md.Vcodec, Acodec: md.Acodec,
			}
		}(i, j)
	}
	wg.Wait()
	return out
}

// loadLibrary memuat cache, memvalidasi via sig (size:mtime), probe hanya yang berubah.
func loadLibrary() map[string]*Video {
	lib.mu.Lock()
	defer lib.mu.Unlock()

	m := map[string]*Video{}
	if b, err := os.ReadFile(cachePath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}

	seen := map[string]bool{}
	var jobs []scanJob
	for _, abs := range findVideos() {
		fi, err := os.Stat(abs)
		if err != nil {
			continue
		}
		rel := relPath(abs)
		seen[rel] = true
		sig := sigOf(fi.Size(), fi.ModTime().Unix())
		if old, ok := m[rel]; ok && old.Sig == sig {
			continue
		}
		jobs = append(jobs, scanJob{abs: abs, rel: rel, size: fi.Size(), mtime: fi.ModTime().Unix()})
	}

	changed := len(jobs) > 0
	for rel := range m {
		if !seen[rel] {
			delete(m, rel)
			changed = true
		}
	}
	for _, v := range probeAll(jobs) {
		m[v.Rel] = v
	}
	if changed {
		saveCache(m)
	}
	return m
}

func saveCache(m map[string]*Video) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(m) != nil {
		return
	}
	tmp := cachePath() + ".tmp"
	if os.WriteFile(tmp, buf.Bytes(), 0o664) == nil {
		_ = os.Rename(tmp, cachePath())
	}
}

// ---------- grouping ----------

type Playlist struct {
	Dir   string
	Label string
	Items []*Video
}

type Grouped struct {
	Root      []*Video
	Playlists []Playlist
	All       []*Video
}

func groupedLibrary() Grouped {
	m := loadLibrary()
	g := Grouped{All: make([]*Video, 0, len(m))}
	folders := map[string][]*Video{}
	for _, v := range m {
		v.Ep = parseEpisode(v.Rel)
		v.Pl = playlistOf(v.Rel)
		g.All = append(g.All, v)
		if v.Pl == "" {
			g.Root = append(g.Root, v)
		} else {
			folders[v.Pl] = append(folders[v.Pl], v)
		}
	}
	sort.Slice(g.Root, func(i, j int) bool { return naturalLess(g.Root[i].Title, g.Root[j].Title) })
	for dir, items := range folders {
		items := items
		sort.Slice(items, func(i, j int) bool {
			a, b := items[i], items[j]
			if a.Ep != nil && b.Ep != nil {
				return *a.Ep < *b.Ep
			}
			return naturalLess(a.Rel, b.Rel)
		})
		g.Playlists = append(g.Playlists, Playlist{Dir: dir, Label: playlistLabel(dir), Items: items})
	}
	sort.Slice(g.Playlists, func(i, j int) bool { return naturalLess(g.Playlists[i].Dir, g.Playlists[j].Dir) })
	return g
}

func newestVideo(all []*Video) *Video {
	var best *Video
	for _, v := range all {
		if best == nil || v.Mtime > best.Mtime {
			best = v
		}
	}
	return best
}

// ---------- nama & label ----------

var (
	reBracket = regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)`)
	reSpaces  = regexp.MustCompile(`\s+`)
	reEpisode = regexp.MustCompile(`(?i)\bE(\d{1,3})\b`)
)

func displayTitle(rel string) string {
	base := filepath.Base(rel)
	b := strings.TrimSuffix(base, filepath.Ext(base))
	b = strings.NewReplacer(".", " ", "_", " ").Replace(b)
	b = reBracket.ReplaceAllString(b, " ")
	b = strings.TrimSpace(reSpaces.ReplaceAllString(b, " "))
	if b == "" {
		return base
	}
	return b
}

func playlistLabel(dir string) string {
	b := filepath.Base(dir)
	b = strings.NewReplacer(".", " ", "_", " ").Replace(b)
	b = strings.TrimSpace(reSpaces.ReplaceAllString(b, " "))
	if b == "" {
		return dir
	}
	return b
}

func playlistOf(rel string) string {
	d := filepath.ToSlash(filepath.Dir(rel))
	if d == "." || d == "" {
		return ""
	}
	return d
}

func parseEpisode(rel string) *int {
	m := reEpisode.FindStringSubmatch(filepath.Base(rel))
	if m == nil {
		return nil
	}
	n, _ := strconv.Atoi(m[1])
	return &n
}

// ---------- poster ----------

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func thumbPath(rel string) string {
	return filepath.Join(cfg.ThumbDir, md5hex(rel)+".jpg")
}

// thumbFor mengembalikan path poster; dibuat malas lewat ffmpeg bila belum ada.
func thumbFor(ctx context.Context, rel string, makeIt bool) (string, bool) {
	f := thumbPath(rel)
	if fi, err := os.Stat(f); err == nil && fi.Size() > 0 {
		return f, true
	}
	if !makeIt {
		return "", false
	}
	abs, ok := safePath(rel)
	if !ok {
		return "", false
	}
	cctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	ss := 2
	acquireFF()
	md := ffprobeMeta(cctx, abs)
	releaseFF()
	if md.Duration > 60 {
		ss = int(md.Duration * 0.20)
	}

	tmp := f + ".tmp.jpg"
	acquireFF()
	defer releaseFF()
	cmd := exec.CommandContext(cctx, "ffmpeg",
		"-v", "quiet", "-y", "-ss", strconv.Itoa(ss), "-i", abs,
		"-frames:v", "1", "-vf", "scale=480:-2", tmp)
	_ = cmd.Run()
	if fi, err := os.Stat(tmp); err == nil && fi.Size() > 0 {
		if os.Rename(tmp, f) == nil {
			return f, true
		}
	}
	_ = os.Remove(tmp)
	return "", false
}

// ---------- subtitle sidecar ----------

func subtitleFor(rel string) (string, string, bool) {
	abs, ok := safePath(rel)
	if !ok {
		return "", "", false
	}
	base := strings.TrimSuffix(abs, filepath.Ext(abs))
	for _, ext := range []string{"vtt", "srt"} {
		if fi, err := os.Stat(base + "." + ext); err == nil && !fi.IsDir() {
			return relPath(base + "." + ext), ext, true
		}
	}
	return "", "", false
}
