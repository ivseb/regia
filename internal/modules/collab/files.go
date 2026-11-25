package collab

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif" // registra i decoder per leggere le dimensioni delle immagini
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FileStore conserva i file su disco indirizzati per sha256: <dir>/ab/cd/abcd…
// Due allegati con lo stesso contenuto condividono il file (deduplica). I file non si
// cancellano mai con l'allegato (eliminazione logica): servono come evidenze.
type FileStore struct {
	dir string
}

// NewFileStore prepara la cartella (permessi 0700).
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return nil, err
	}
	return &FileStore{dir: dir}, nil
}

// Blob è un file appena salvato.
type Blob struct {
	SHA256 string
	Size   int64
	Mime   string
	Width  *int
	Height *int
}

// ErrTooLarge: il file supera il limite configurato.
var ErrTooLarge = errors.New("file troppo grande")

var hexRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (f *FileStore) path(sha string) string {
	return filepath.Join(f.dir, sha[0:2], sha[2:4], sha)
}

// Put legge r fino a limit byte, calcola lo sha256 e salva il file se non c'è già.
// Il tipo si rileva dal contenuto (l'estensione del nome serve solo a distinguere i
// formati che il contenuto non distingue, come i documenti Office dentro uno zip).
func (f *FileStore) Put(r io.Reader, limit int64, name string) (Blob, error) {
	tmp, err := os.CreateTemp(filepath.Join(f.dir, "tmp"), "up-*")
	if err != nil {
		return Blob{}, err
	}
	defer os.Remove(tmp.Name()) // dopo il rename non esiste più: l'errore si ignora
	h := sha256.New()
	head := &headWriter{max: 512}
	n, err := io.Copy(io.MultiWriter(tmp, h, head), io.LimitReader(r, limit+1))
	if err != nil {
		tmp.Close()
		return Blob{}, err
	}
	if n > limit {
		tmp.Close()
		return Blob{}, ErrTooLarge
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Blob{}, err
	}
	if err := tmp.Close(); err != nil {
		return Blob{}, err
	}
	b := Blob{SHA256: hex.EncodeToString(h.Sum(nil)), Size: n, Mime: DetectMime(head.buf.Bytes(), name)}
	dst := f.path(b.SHA256)
	if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return Blob{}, err
		}
		if err := os.Rename(tmp.Name(), dst); err != nil {
			return Blob{}, err
		}
	} else if err != nil {
		return Blob{}, err
	}
	if b.Mime == "image/png" || b.Mime == "image/jpeg" || b.Mime == "image/gif" {
		if fh, err := os.Open(dst); err == nil {
			if cfg, _, err := image.DecodeConfig(fh); err == nil {
				b.Width, b.Height = &cfg.Width, &cfg.Height
			}
			fh.Close()
		}
	}
	return b, nil
}

// Open apre il file con lo sha256 indicato.
func (f *FileStore) Open(sha string) (*os.File, error) {
	if !hexRe.MatchString(sha) {
		return nil, os.ErrNotExist
	}
	return os.Open(f.path(sha))
}

type headWriter struct {
	buf bytes.Buffer
	max int
}

func (w *headWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		w.buf.Write(p[:room])
	}
	return len(p), nil
}

// Formati che il contenuto da solo non distingue: si guarda l'estensione, ma solo se il
// contenuto è compatibile (un file .docx deve essere davvero uno zip).
var zipByExt = map[string]string{
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".odt":  "application/vnd.oasis.opendocument.text",
	".ods":  "application/vnd.oasis.opendocument.spreadsheet",
	".odp":  "application/vnd.oasis.opendocument.presentation",
}
var textByExt = map[string]string{".csv": "text/csv", ".md": "text/markdown", ".json": "application/json", ".log": "text/plain"}
var oleByExt = map[string]string{".doc": "application/msword", ".xls": "application/vnd.ms-excel", ".ppt": "application/vnd.ms-powerpoint", ".msg": "application/vnd.ms-outlook"}

// DetectMime rileva il tipo dal contenuto (http.DetectContentType, algoritmo WHATWG).
func DetectMime(head []byte, name string) string {
	ct, _, _ := strings.Cut(http.DetectContentType(head), ";")
	ext := strings.ToLower(filepath.Ext(name))
	switch ct {
	case "application/zip":
		if m, ok := zipByExt[ext]; ok {
			return m
		}
	case "text/plain":
		if m, ok := textByExt[ext]; ok {
			return m
		}
	case "application/octet-stream":
		if m, ok := oleByExt[ext]; ok && bytes.HasPrefix(head, []byte{0xD0, 0xCF, 0x11, 0xE0}) {
			return m
		}
	}
	return ct
}

// InlineImage: immagini che il browser può mostrare senza rischi (niente SVG, che è un
// documento con script). Tutto il resto si scarica come allegato.
func InlineImage(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

// ServedType è il Content-Type del download: i formati attivi (HTML, XML, SVG, script)
// non si servono mai come tali, anche se scaricati come allegato.
func ServedType(mime string) string {
	switch {
	case strings.Contains(mime, "html"), strings.Contains(mime, "xml"), strings.Contains(mime, "javascript"), strings.Contains(mime, "svg"):
		return "application/octet-stream"
	case strings.HasPrefix(mime, "text/"):
		return mime + "; charset=utf-8"
	}
	return mime
}

// CleanName ripulisce il nome del file: niente cartelle, caratteri di controllo o nomi vuoti.
func CleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 200 {
		ext := filepath.Ext(name)
		if len([]rune(ext)) > 20 {
			ext = ""
		}
		name = string(r[:200-len([]rune(ext))]) + ext
	}
	if name == "" || name == "." || name == ".." || name == "/" {
		return "file"
	}
	return name
}
