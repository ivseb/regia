package itest

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/collab"
	"regia/internal/modules/demand"
	"regia/internal/platform/auth"
	"regia/internal/platform/backup"
	"regia/internal/platform/db"
	"regia/internal/platform/doctor"
	"regia/internal/platform/embeddedpg"
	"regia/internal/platform/secretbox"
	"regia/migrations"
)

// Pronti per la produzione (piano, passo 17): migrazioni fuori transazione, backup e
// ripristino, backup automatico prima delle migrazioni, doctor.

var quietLog = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

// scratchDB crea un database vuoto sullo stesso server dei test e ne restituisce l'URL.
func scratchDB(t *testing.T, prefix string) string {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	if _, err := needDB(t).Owner.Exec(ctx, "create database "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Owner.Exec(context.Background(), "drop database if exists "+name+" with (force)")
	})
	return strings.Replace(testPG.OwnerURL(), "dbname="+embeddedpg.Database, "dbname="+name, 1)
}

func scratchPool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestMigrazioniFuoriTransazione(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t, scratchDB(t, "mig"))
	fsys := fstest.MapFS{
		"0001_tabelle.sql": {Data: []byte(`
create table grande (id bigint generated always as identity primary key, v int not null);
insert into grande (v) select g % 100 from generate_series(1, 1000) g;
create table storia (at timestamptz not null, x int) partition by range (at);
create table storia_2026 partition of storia for values from ('2026-01-01') to ('2027-01-01');
create table storia_default partition of storia default;
insert into storia values ('2026-05-01', 1), ('2030-01-01', 2);`)},
		"0002_indici.sql": {Data: []byte(`-- Indici su tabelle grandi, senza bloccare le scritture.
-- regia:no-transaction
create index concurrently if not exists grande_v_idx on grande (v);
-- Fallisce finché ci sono duplicati: lascia un indice INVALID.
create unique index concurrently if not exists grande_v_uq on grande (v);`)},
	}
	if _, err := db.Migrate(ctx, pool, fsys, quietLog); err == nil || !strings.Contains(err.Error(), "istruzione 2 di 2") {
		t.Fatalf("attesa la violazione di unicità nella seconda istruzione: %v", err)
	}
	valid := func(name string) (exists, ok bool) {
		t.Helper()
		err := pool.QueryRow(ctx, `select true, indisvalid from pg_index where indexrelid = to_regclass($1)`, name).Scan(&exists, &ok)
		if err != nil && !strings.Contains(err.Error(), "no rows") {
			t.Fatal(err)
		}
		return exists, ok
	}
	if e, ok := valid("grande_v_idx"); !e || !ok {
		t.Fatal("la prima istruzione, fuori transazione, deve restare applicata")
	}
	if e, ok := valid("grande_v_uq"); !e || ok {
		t.Fatalf("atteso un indice INVALID lasciato dal tentativo fallito (esiste=%v valido=%v)", e, ok)
	}
	var recorded bool
	if err := pool.QueryRow(ctx, "select exists (select 1 from schema_migrations where version = '0002_indici')").Scan(&recorded); err != nil || recorded {
		t.Fatalf("migrazione fallita registrata come applicata: %v %v", recorded, err)
	}

	// Corretti i dati, il nuovo tentativo elimina l'indice non valido e lo ricostruisce.
	if _, err := pool.Exec(ctx, "delete from grande where id > 100"); err != nil {
		t.Fatal(err)
	}
	fsys["0003_partizioni.sql"] = &fstest.MapFile{Data: []byte(`-- regia:no-transaction
create index concurrently if not exists storia_x_idx on storia (x);`)}
	n, err := db.Migrate(ctx, pool, fsys, quietLog)
	if err != nil || n != 2 {
		t.Fatalf("secondo tentativo: %d %v", n, err)
	}
	if e, ok := valid("grande_v_uq"); !e || !ok {
		t.Fatal("indice unico non ricostruito")
	}
	// Tabella partizionata: indice sulla madre valido, una copia agganciata per partizione.
	if e, ok := valid("storia_x_idx"); !e || !ok {
		t.Fatal("indice della tabella partizionata non valido")
	}
	var attached int
	if err := pool.QueryRow(ctx, `select count(*) from pg_inherits where inhparent = 'storia_x_idx'::regclass`).Scan(&attached); err != nil || attached != 2 {
		t.Fatalf("indici agganciati alle partizioni: %d %v", attached, err)
	}
	// Una partizione nuova eredita l'indice.
	if _, err := pool.Exec(ctx, "create table storia_2027 partition of storia for values from ('2027-01-01') to ('2028-01-01')"); err == nil {
		if err := pool.QueryRow(ctx, `select count(*) from pg_inherits where inhparent = 'storia_x_idx'::regclass`).Scan(&attached); err != nil || attached != 3 {
			t.Fatalf("partizione nuova senza indice: %d %v", attached, err)
		}
	}

	// Stato delle migrazioni.
	st, err := db.Status(ctx, pool, fsys)
	if err != nil || len(st.Applied) != 3 || len(st.Pending) != 0 || st.Fresh() {
		t.Fatalf("stato: %+v %v", st, err)
	}
	fsys["0004_nuova.sql"] = &fstest.MapFile{Data: []byte("select 1;")}
	fsys["0001_tabelle.sql"] = &fstest.MapFile{Data: []byte("-- modificata")}
	delete(fsys, "0002_indici.sql")
	st, err = db.Status(ctx, pool, fsys)
	if err != nil || fmt.Sprint(st.Pending) != "[0004_nuova]" || fmt.Sprint(st.Modified) != "[0001_tabelle]" || fmt.Sprint(st.Unknown) != "[0002_indici]" {
		t.Fatalf("stato con differenze: %+v %v", st, err)
	}
	if _, err := db.Migrate(ctx, pool, fsys, quietLog); err == nil || !strings.Contains(err.Error(), "modificata") {
		t.Fatalf("una migrazione modificata deve bloccare: %v", err)
	}
}

// fingerprint riassume i dati di alcune tabelle per confrontarli dopo il ripristino.
func fingerprint(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var out []string
	for _, table := range []string{"tenants", "accounts", "members", "elements", "mfa_recovery_codes", "schema_migrations"} {
		var sum string
		if err := pool.QueryRow(context.Background(),
			fmt.Sprintf("select coalesce(md5(string_agg(t::text, '|' order by t::text)), '') || ':' || count(*) from %s t", table)).Scan(&sum); err != nil {
			t.Fatal(err)
		}
		out = append(out, table+"="+sum)
	}
	return strings.Join(out, " ")
}

func TestBackupRestore(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	// Come in produzione: la chiave applicativa sta nella cartella dati.
	key, err := secretbox.LoadKey(filepath.Join(testDataDir, "keys", "secrets.key"))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatal(err)
	}
	e := newAdminEnvBox(t, box)
	u := e.createMember("backup", "editor")
	email := e.email("backup")
	secret := strings.ReplaceAll(u.do("POST", "/api/v1/me/mfa/setup", nil, http.StatusOK)["secret"].(string), " ", "")
	now := time.Now()
	u.do("POST", "/api/v1/me/mfa/enable", map[string]any{"code": codeAt(t, secret, now)}, http.StatusOK)
	// Un allegato su disco.
	store, err := collab.NewFileStore(filepath.Join(testDataDir, "files"))
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("evidenza del collaudo " + e.slug)
	blob, err := store.Put(bytes.NewReader(content), 1<<20, "evidenza.txt")
	if err != nil {
		t.Fatal(err)
	}
	blobRel := filepath.Join("files", blob.SHA256[0:2], blob.SHA256[2:4], blob.SHA256)

	// Backup cifrato con il server in esecuzione (Postgres dei test avviato e in uso).
	before := fingerprint(t, d.Owner)
	out := filepath.Join(t.TempDir(), "backup.tar.age")
	const pass = "passphrase del collaudo"
	res, err := backup.Create(ctx, backup.Options{
		DataDir: testDataDir, OwnerURL: testPG.OwnerURL(), BinDir: testPG.BinDir(), Embedded: true,
		Out: out, Passphrase: pass, WorkFactor: 10, Version: "test", Commit: "itest",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Keys != 1 || res.Files < 1 || !res.Encrypted || res.Manifest.LastMigration() == "" {
		t.Fatalf("backup: %+v", res)
	}
	if fi, err := os.Stat(out); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("permessi del backup: %v %v", fi.Mode(), err)
	}
	if _, err := backup.Verify(out, "passphrase sbagliata!"); err == nil {
		t.Fatal("passphrase sbagliata accettata")
	}
	known, err := db.Versions(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	// Compatibilità: un binario che non conosce l'ultima migrazione rifiuta il backup prima di scrivere.
	if _, err := backup.Check(out, pass, known[:len(known)-1]); err == nil || !strings.Contains(err.Error(), "più recente") {
		t.Fatalf("controllo di versione: %v", err)
	}

	// Ripristino in una cartella dati nuova, con un Postgres incluso nuovo.
	dst := t.TempDir()
	pg2, err := embeddedpg.New(dst, os.Getenv("REGIA_PG_BIN"), quietLog)
	if err != nil {
		t.Fatal(err)
	}
	if err := pg2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer pg2.Stop(10 * time.Second) //nolint:errcheck
	s, err := backup.Restore(ctx, backup.RestoreOptions{
		In: out, Passphrase: pass, DataDir: dst, OwnerURL: pg2.OwnerURL(), BinDir: pg2.BinDir(), Embedded: true, Known: known, Log: quietLog,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Keys != 1 || s.Files != res.Files {
		t.Fatalf("ripristino: %+v", s)
	}
	d2, err := db.Open(ctx, pg2.OwnerURL(), pg2.AppURL())
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	if n, err := db.Migrate(ctx, d2.Owner, migrations.FS, quietLog); err != nil || n != 0 {
		t.Fatalf("migrazioni dopo il ripristino: %d %v", n, err)
	}
	if after := fingerprint(t, d2.Owner); after != before {
		t.Fatalf("dati diversi dopo il ripristino:\nprima %s\ndopo  %s", before, after)
	}
	got, err := os.ReadFile(filepath.Join(dst, blobRel))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("allegato: %v", err)
	}
	key2, err := os.ReadFile(filepath.Join(dst, "keys", "secrets.key"))
	if err != nil || !bytes.Equal(key2, key) {
		t.Fatal("chiave applicativa non ripristinata")
	}
	// Un secondo ripristino sullo stesso database si rifiuta.
	if _, err := backup.Restore(ctx, backup.RestoreOptions{In: out, Passphrase: pass, DataDir: t.TempDir(), OwnerURL: pg2.OwnerURL(),
		BinDir: pg2.BinDir(), Embedded: true, Known: known}); err == nil || !strings.Contains(err.Error(), "non è vuoto") {
		t.Fatalf("ripristino su un database pieno: %v", err)
	}

	// L'MFA funziona ancora sull'istanza ripristinata, grazie alla chiave del backup.
	box2, err := secretbox.New(key2)
	if err != nil {
		t.Fatal(err)
	}
	base := apiServer(t, d2, box2)
	c := anonClient(t, base)
	r := c.do("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": testPassword}, http.StatusOK)
	if r["mfa_required"] != true {
		t.Fatalf("login dopo il ripristino: %v", r)
	}
	c.do("POST", "/api/v1/auth/mfa/verify", map[string]any{"code": codeAt(t, secret, now.Add(30*time.Second))}, http.StatusOK)
	c.do("GET", "/api/v1/me", nil, http.StatusOK)

	// Con un'altra chiave i segreti non si decifrano: doctor lo segnala.
	rep := &doctor.Report{}
	doctor.Database(ctx, rep, d2.Owner, migrations.FS, dst, false, embeddedpg.MinMajor, embeddedpg.RecommendedMajor)
	// Altri test cifrano l'MFA con chiavi casuali: si accetta un'avvertenza, non un errore.
	if rep.Failed() || !strings.Contains(reportText(rep), "secrets.key decifra") {
		t.Fatalf("doctor dopo il ripristino:\n%s", reportText(rep))
	}
	if err := os.WriteFile(filepath.Join(dst, "keys", "secrets.key"), bytes.Repeat([]byte{7}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	rep = &doctor.Report{}
	doctor.Database(ctx, rep, d2.Owner, migrations.FS, dst, false, embeddedpg.MinMajor, embeddedpg.RecommendedMajor)
	if !rep.Failed() || !strings.Contains(reportText(rep), "non decifra") {
		t.Fatalf("doctor con la chiave sbagliata:\n%s", reportText(rep))
	}
	if err := os.Remove(filepath.Join(dst, "keys", "secrets.key")); err != nil {
		t.Fatal(err)
	}
	rep = &doctor.Report{}
	doctor.Database(ctx, rep, d2.Owner, migrations.FS, dst, false, embeddedpg.MinMajor, embeddedpg.RecommendedMajor)
	if !strings.Contains(reportText(rep), "secrets.key manca") {
		t.Fatalf("doctor senza chiave:\n%s", reportText(rep))
	}
}

// Postgres esterno (DATABASE_URL): stesso server, database nuovo come destinazione.
func TestBackupRestoreEsterno(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	before := fingerprint(t, d.Owner)
	out := filepath.Join(t.TempDir(), "backup.tar")
	res, err := backup.Create(ctx, backup.Options{DataDir: testDataDir, OwnerURL: testPG.OwnerURL(), BinDir: testPG.BinDir(), Out: out, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Encrypted || res.Manifest.Mode != "external" {
		t.Fatalf("backup: %+v", res.Manifest)
	}
	known, _ := db.Versions(migrations.FS)
	target := scratchDB(t, "ripristino")
	dst := t.TempDir()
	if _, err := backup.Restore(ctx, backup.RestoreOptions{In: out, DataDir: dst, OwnerURL: target, BinDir: testPG.BinDir(), Known: known}); err != nil {
		t.Fatal(err)
	}
	if after := fingerprint(t, scratchPool(t, target)); after != before {
		t.Fatalf("dati diversi dopo il ripristino:\nprima %s\ndopo  %s", before, after)
	}
	// keys/ e files/ ci sono già: senza --force il ripristino si ferma, con --force li sposta da parte.
	other := scratchDB(t, "ripristino")
	if _, err := backup.Restore(ctx, backup.RestoreOptions{In: out, DataDir: dst, OwnerURL: other, BinDir: testPG.BinDir(), Known: known}); err == nil ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("cartelle esistenti: %v", err)
	}
	if _, err := backup.Restore(ctx, backup.RestoreOptions{In: out, DataDir: dst, OwnerURL: other, BinDir: testPG.BinDir(), Known: known, Force: true}); err != nil {
		t.Fatal(err)
	}
	aside, _ := filepath.Glob(filepath.Join(dst, "pre-restore-*", "keys", "secrets.key"))
	if len(aside) != 1 {
		t.Fatalf("chiavi precedenti non spostate da parte: %v", aside)
	}
}

func TestBackupAutomatico(t *testing.T) {
	ctx := context.Background()
	url := scratchDB(t, "auto")
	pool := scratchPool(t, url)
	data := t.TempDir()
	dir := filepath.Join(data, "backups")
	fsys := fstest.MapFS{"0001_a.sql": {Data: []byte("create table a (x int);")}}
	o := backup.Options{DataDir: data, OwnerURL: url, BinDir: testPG.BinDir(), Version: "test"}

	// Primo avvio: niente da salvare.
	a, err := backup.BeforeMigrate(ctx, pool, fsys, o, dir, 2)
	if err != nil || a.Result != nil {
		t.Fatalf("database vuoto: %+v %v", a, err)
	}
	if _, err := db.Migrate(ctx, pool, fsys, quietLog); err != nil {
		t.Fatal(err)
	}
	// Nessuna migrazione nuova: niente backup.
	if a, err = backup.BeforeMigrate(ctx, pool, fsys, o, dir, 2); err != nil || a.Result != nil {
		t.Fatalf("senza migrazioni nuove: %+v %v", a, err)
	}
	// Migrazioni nuove: backup, e la retention tiene gli ultimi due.
	for i := 2; i <= 4; i++ {
		fsys[fmt.Sprintf("000%d_n.sql", i)] = &fstest.MapFile{Data: []byte(fmt.Sprintf("create table t%d (x int);", i))}
		a, err := backup.BeforeMigrate(ctx, pool, fsys, o, dir, 2)
		if err != nil || a.Result == nil || fmt.Sprint(a.Status.Pending) != fmt.Sprintf("[000%d_n]", i) {
			t.Fatalf("migrazione %d: %+v %v", i, a, err)
		}
		if _, err := db.Migrate(ctx, pool, fsys, quietLog); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond) // il nome del file ha la precisione del secondo
	}
	names, _ := backup.List(dir, backup.AutoPrefix)
	if len(names) != 2 {
		t.Fatalf("retention: %v", names)
	}
	// L'ultimo backup è stato fatto prima della 0004: non la contiene.
	s, err := backup.Verify(filepath.Join(dir, names[0]), "")
	if err != nil || s.Manifest.LastMigration() != "0003_n" {
		t.Fatalf("contenuto dell'ultimo backup: %+v %v", s, err)
	}
}

func TestDoctor(t *testing.T) {
	d := needDB(t)
	rep := &doctor.Report{}
	doctor.DataDir(rep, testDataDir)
	major := doctor.Database(context.Background(), rep, d.Owner, migrations.FS, testDataDir, false, embeddedpg.MinMajor, embeddedpg.RecommendedMajor)
	doctor.Tools(rep, testPG.BinDir(), major)
	text := reportText(rep)
	// Le chiavi dipendono dai test eseguiti prima (ognuno ha la sua): qui contano le altre aree.
	for _, c := range rep.Checks {
		if c.Level == doctor.Fail && c.Area != "chiavi" {
			t.Fatalf("doctor sull'istanza dei test:\n%s", text)
		}
	}
	for _, want := range []string{"migrazioni", "nessuna in attesa", "pg_dump", "pg_restore", "orologio"} {
		if !strings.Contains(text, want) {
			t.Errorf("manca %q nel rapporto:\n%s", want, text)
		}
	}
}

func reportText(r *doctor.Report) string {
	var b strings.Builder
	r.Print(&b)
	return b.String()
}

// apiServer avvia l'API su un database qualunque (qui quello ripristinato).
func apiServer(t *testing.T, d *db.DB, box *secretbox.Box) string {
	t.Helper()
	eng := engine.New()
	ap := approval.Register(eng)
	svc := auth.NewService(d, time.Hour)
	svc.SetSecretBox(box)
	srv := api.New(api.Options{
		DB: d, Auth: svc, Engine: eng, Approvals: ap, Demand: demand.Register(eng, ap), Arch: architecture.Register(eng),
		Log: quietLog, Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, SecretBox: box,
	})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	t.Cleanup(srv.Close)
	return hs.URL
}
