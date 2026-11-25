package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spa serve il frontend compilato. I file con hash nel nome (assets/) si mettono in
// cache per un anno; index.html mai, così un aggiornamento è visibile subito.
// I percorsi che non sono file tornano index.html (routing lato client).
func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeProblem(w, r, http.StatusMethodNotAllowed, "route.method_not_allowed", "metodo non consentito")
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if fi, err := fs.Stat(s.web, p); err == nil && !fi.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(s.web, "index.html")
		if err != nil {
			index = []byte(placeholder)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

const placeholder = `<!doctype html><html lang="it"><meta charset="utf-8"><title>Regia</title>
<p>Il frontend non è incluso in questa build. Compilalo con <code>make web</code> e ricompila il binario.</p></html>`
