package backup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Archivio senza Postgres: manifest, dump finto, chiavi e allegati, in chiaro e cifrato.
func TestArchiveRoundTrip(t *testing.T) {
	data := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(data, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("keys/secrets.key", strings.Repeat("k", 32))
	write("files/ab/cd/abcdef", "allegato")
	write("files/tmp/up-123", "caricamento in corso") // escluso
	dump := filepath.Join(t.TempDir(), "dump")
	if err := os.WriteFile(dump, []byte("PGDMP finto"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := Manifest{Format: Format, App: "regia", Version: "test", Migrations: []string{"0001_schema"}}

	for _, pass := range []string{"", "una passphrase lunga"} {
		out := filepath.Join(t.TempDir(), "b.tar")
		f, err := os.Create(out)
		if err != nil {
			t.Fatal(err)
		}
		res := &Result{}
		if err := writeArchive(f, Options{DataDir: data, Passphrase: pass, WorkFactor: 10}, &m, dump, res); err != nil {
			t.Fatal(err)
		}
		f.Close()
		if res.Keys != 1 || res.Files != 1 {
			t.Fatalf("contenuto: %+v", res)
		}
		enc, _ := Encrypted(out)
		if enc != (pass != "") {
			t.Fatalf("cifrato=%v con passphrase %q", enc, pass)
		}
		if pass != "" {
			if _, err := Verify(out, ""); !errors.Is(err, ErrPassphrase) {
				t.Fatalf("senza passphrase: %v", err)
			}
			if _, err := Verify(out, "sbagliata"); !errors.Is(err, ErrPassphrase) {
				t.Fatalf("passphrase sbagliata: %v", err)
			}
		}
		s, err := Verify(out, pass)
		if err != nil {
			t.Fatal(err)
		}
		if s.Keys != 1 || s.Files != 1 || s.DumpBytes != int64(len("PGDMP finto")) || s.Manifest.Version != "test" {
			t.Fatalf("verifica: %+v", s)
		}
	}

	// Un archivio alterato non passa la verifica.
	out := filepath.Join(t.TempDir(), "b.tar")
	f, _ := os.Create(out)
	if err := writeArchive(f, Options{DataDir: data}, &m, dump, &Result{}); err != nil {
		t.Fatal(err)
	}
	f.Close()
	b, _ := os.ReadFile(out)
	i := strings.Index(string(b), "PGDMP finto")
	b[i] = 'X'
	if err := os.WriteFile(out, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(out, ""); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("archivio alterato: %v", err)
	}
}

func TestCompatible(t *testing.T) {
	known := []string{"0001_schema", "0002_roles"}
	if err := Compatible(Manifest{Format: 1, Migrations: []string{"0001_schema"}}, known); err != nil {
		t.Fatalf("backup più vecchio: %v", err)
	}
	if err := Compatible(Manifest{Format: 1, Version: "v9", Migrations: []string{"0001_schema", "0099_futura"}}, known); err == nil ||
		!strings.Contains(err.Error(), "0099_futura") {
		t.Fatalf("backup più recente: %v", err)
	}
	if err := Compatible(Manifest{Format: 2}, known); err == nil {
		t.Fatal("formato futuro accettato")
	}
}

func TestSafeEntry(t *testing.T) {
	for name, ok := range map[string]bool{
		"manifest.json": true, "db/mytrack.dump": true, "SHA256SUMS": true,
		"keys/secrets.key": true, "files/ab/cd/abcd": true,
		"keys/sub/x": false, "../etc/passwd": false, "files/../../x": false, "/etc/passwd": false,
		"pg/PG_VERSION": false, "files/./x": false,
	} {
		if safeEntry(name) != ok {
			t.Errorf("%s: atteso %v", name, ok)
		}
	}
}

func TestListPrune(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	for i := range 5 {
		if err := os.WriteFile(filepath.Join(dir, FileName("auto", base.Add(time.Duration(i)*time.Hour), i%2 == 0)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, other := range []string{"regia-20261124T100000Z.tar", "auto-nota.txt", "auto-20261124T100000Z.tar.tmp"} {
		_ = os.WriteFile(filepath.Join(dir, other), nil, 0o600)
	}
	names, _ := List(dir, "auto")
	if len(names) != 5 || names[0] != "auto-20261124T140000Z.tar.age" {
		t.Fatalf("elenco: %v", names)
	}
	removed, err := Prune(dir, "auto", 2)
	if err != nil || len(removed) != 3 {
		t.Fatalf("pulizia: %v %v", removed, err)
	}
	names, _ = List(dir, "auto")
	if len(names) != 2 || names[1] != "auto-20261124T130000Z.tar" {
		t.Fatalf("dopo la pulizia: %v", names)
	}
	if _, err := os.Stat(filepath.Join(dir, "regia-20261124T100000Z.tar")); err != nil {
		t.Fatal("cancellato un backup con un altro prefisso")
	}
}

func TestFilterTOC(t *testing.T) {
	toc := `;
; Archive created at 2026-11-24 10:00:00 UTC
2; 3079 16390 EXTENSION - vector
4100; 0 0 COMMENT - EXTENSION vector
3; 3079 16385 EXTENSION - pgcrypto
230; 1259 17000 TABLE public embeddings mytrack_owner
231; 1259 17010 TABLE public elements mytrack_owner
5000; 0 17000 TABLE DATA public embeddings mytrack_owner
5001; 0 17010 TABLE DATA public elements mytrack_owner
6000; 2606 17100 CONSTRAINT public embeddings embeddings_pkey mytrack_owner
6001; 1259 17200 INDEX public embeddings_hnsw mytrack_owner
6002; 2606 17300 FK CONSTRAINT public embeddings embeddings_tenant_id_element_id_fkey mytrack_owner
6003; 3256 17400 POLICY public embeddings tenant_isolation mytrack_owner
6004; 0 17000 ROW SECURITY public embeddings mytrack_owner
6005; 0 0 ACL public TABLE embeddings mytrack_owner
6006; 0 0 ACL public TABLE elements mytrack_owner`
	got := FilterTOC(toc, []string{"vector"})
	for _, l := range strings.Split(got, "\n") {
		kept := !strings.HasPrefix(l, ";")
		if kept && (strings.Contains(l, "vector") || strings.Contains(l, "embeddings")) {
			t.Errorf("riga non filtrata: %s", l)
		}
		if !kept && (strings.Contains(l, "elements") || strings.Contains(l, "pgcrypto")) {
			t.Errorf("riga filtrata per errore: %s", l)
		}
	}
}
