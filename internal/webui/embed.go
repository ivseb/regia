// Package webui include nel binario il frontend compilato da web/ (vite build).
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS restituisce il contenuto compilato. Se il frontend non è stato compilato
// contiene solo LEGGIMI.txt e il server mostra una pagina segnaposto.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist/app")
	if err != nil {
		panic(err)
	}
	return sub
}
