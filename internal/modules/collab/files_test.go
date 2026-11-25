package collab

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectMime(t *testing.T) {
	zip := []byte("PK\x03\x04\x14\x00\x06\x00")
	cases := []struct {
		head []byte
		name string
		want string
	}{
		{[]byte("\x89PNG\r\n\x1a\n0000"), "a.jpg", "image/png"},
		{[]byte("%PDF-1.7\n"), "contratto.pdf", "application/pdf"},
		{zip, "offerta.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{zip, "offerta.pdf", "application/zip"}, // l'estensione non basta a cambiare il tipo
		{[]byte("a;b;c\n1;2;3\n"), "dati.csv", "text/csv"},
		{[]byte("<html><body>x</body></html>"), "foto.png", "text/html"},
		{[]byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"), "logo.svg", "text/plain"}, // mai image/svg+xml: niente script in linea
	}
	for _, c := range cases {
		if got := DetectMime(c.head, c.name); got != c.want {
			t.Errorf("DetectMime(%q, %s) = %s, atteso %s", c.head[:4], c.name, got, c.want)
		}
	}
}

func TestServedTypeAndInline(t *testing.T) {
	for _, m := range []string{"text/html", "text/xml", "image/svg+xml", "application/javascript"} {
		if ServedType(m) != "application/octet-stream" || InlineImage(m) {
			t.Errorf("%s servito come attivo", m)
		}
	}
	if ServedType("text/csv") != "text/csv; charset=utf-8" || !InlineImage("image/jpeg") {
		t.Error("tipi sicuri trattati male")
	}
}

func TestCleanName(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd":         "passwd",
		`C:\Users\me\offerta.docx`: "offerta.docx",
		"rel\x00azione\n.pdf":      "relazione.pdf",
		"":                         "file",
		"..":                       "file",
	}
	for in, want := range cases {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, atteso %q", in, got, want)
		}
	}
	long := strings.Repeat("à", 300) + ".pdf"
	if got := CleanName(long); len([]rune(got)) != 200 || !strings.HasSuffix(got, ".pdf") {
		t.Errorf("nome lungo: %d caratteri", len([]rune(got)))
	}
}

func TestFileStoreDedupAndLimit(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := fs.Put(bytes.NewReader([]byte("ciao")), 10, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, err := fs.Put(bytes.NewReader([]byte("ciao")), 10, "b.txt")
	if err != nil || a.SHA256 != b.SHA256 || a.Size != 4 || a.Mime != "text/plain" {
		t.Fatalf("deduplica: %v %v %v", a, b, err)
	}
	if _, err := fs.Put(bytes.NewReader(make([]byte, 11)), 10, "c.bin"); err != ErrTooLarge {
		t.Fatalf("limite: %v", err)
	}
	if left, _ := os.ReadDir(filepath.Join(dir, "tmp")); len(left) != 0 {
		t.Fatalf("file temporanei rimasti: %d", len(left))
	}
	if _, err := fs.Open("../../x"); err == nil {
		t.Fatal("Open accetta percorsi arbitrari")
	}
}

func TestExcerpt(t *testing.T) {
	got := Excerpt("Ciao **Sara**, vedi [il documento](https://x.it) e `codice`", 200)
	if got != "Ciao Sara, vedi il documento e codice" {
		t.Fatalf("estratto: %q", got)
	}
	if got := Excerpt(strings.Repeat("parola ", 50), 20); len([]rune(got)) > 20 || !strings.HasSuffix(got, "…") {
		t.Fatalf("estratto lungo: %q", got)
	}
}
