package handlers

import (
	"io/fs"
	"net/http"
)

// webHeaders applies to the landing page and its assets only. Redirects are
// the hot path and skip this entirely.
func webHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
			"font-src https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
}

func serveIndex(web fs.FS) http.HandlerFunc {
	page, _ := fs.ReadFile(web, "index.html")
	return func(w http.ResponseWriter, r *http.Request) {
		webHeaders(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(page)
	}
}

func staticHandler(web fs.FS) http.Handler {
	files := http.FileServer(http.FS(web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webHeaders(w)
		w.Header().Set("Cache-Control", "no-cache") // assets aren't fingerprinted, so always revalidate
		files.ServeHTTP(w, r)
	})
}
