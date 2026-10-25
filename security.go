package main

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// ---------- path & id validation (anti path-traversal) ----------

// videoID = base64url dari nama relatif; kunci stabil untuk URL & cache.
func videoID(rel string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(rel))
}

// idToRel membalik videoID dengan decode ketat; tolak apa pun yang mencurigakan.
func idToRel(id string) (string, bool) {
	if id == "" || len(id) > 8192 {
		return "", false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(id)
	if err != nil {
		return "", false
	}
	rel := string(b)
	if rel == "" || strings.ContainsRune(rel, 0) || strings.Contains(rel, "..") {
		return "", false
	}
	return rel, true
}

// within melaporkan p di dalam root (aman terhadap prefix palsu seperti /lib-evil).
func within(root, p string) bool {
	root = filepath.Clean(root)
	return p == root || strings.HasPrefix(p, root+string(os.PathSeparator))
}

// safePath memetakan rel -> path absolut di dalam LibDir, atau menolak.
// Berlapis: format rel, komponen tersembunyi, containment, dan symlink.
func safePath(rel string) (string, bool) {
	if rel == "" || strings.ContainsRune(rel, 0) || strings.Contains(rel, "\\") {
		return "", false
	}
	if strings.HasPrefix(rel, "/") {
		return "", false
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || strings.HasPrefix(part, ".") {
			return "", false // tolak komponen kosong & tersembunyi (".", "._x")
		}
	}
	abs := filepath.Clean(filepath.Join(cfg.LibDir, filepath.FromSlash(clean)))
	if !within(cfg.LibDir, abs) {
		return "", false
	}
	real, err := filepath.EvalSymlinks(abs) // butuh file ada
	if err != nil {
		return "", false
	}
	if !within(cfg.LibDir, real) {
		return "", false // symlink menunjuk ke luar library
	}
	return real, true
}

// ---------- natural sort (padanan strnatcasecmp) ----------

func naturalLess(a, b string) bool {
	ar, br := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ar) && j < len(br) {
		if unicode.IsDigit(ar[i]) && unicode.IsDigit(br[j]) {
			ii, jj := i, j
			for ii < len(ar) && unicode.IsDigit(ar[ii]) {
				ii++
			}
			for jj < len(br) && unicode.IsDigit(br[jj]) {
				jj++
			}
			na, _ := strconv.Atoi(string(ar[i:ii]))
			nb, _ := strconv.Atoi(string(br[j:jj]))
			if na != nb {
				return na < nb
			}
			i, j = ii, jj
			continue
		}
		la, lb := unicode.ToLower(ar[i]), unicode.ToLower(br[j])
		if la != lb {
			return la < lb
		}
		i++
		j++
	}
	return len(ar)-i < len(br)-j
}

// ---------- security headers ----------

func withSecurity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; media-src 'self'; "+
				"style-src 'self'; script-src 'self'; object-src 'none'; "+
				"base-uri 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// sameOrigin menolak permintaan lintas-situs untuk endpoint yang mengubah state.
func sameOrigin(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" {
		return sfs == "same-origin" || sfs == "none"
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true // klien non-browser
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

// ---------- concurrency guard untuk ffmpeg/ffprobe ----------

var ffSem chan struct{}

func acquireFF() { ffSem <- struct{}{} }
func releaseFF() { <-ffSem }
