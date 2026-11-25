package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationLock è la chiave dell'advisory lock: più istanze che partono insieme
// applicano le migrazioni una volta sola.
const migrationLock = 0x6d79747261636b // "mytrack"

// NoTransaction è la direttiva che, in testa a un file di migrazione, lo fa eseguire
// fuori transazione, un'istruzione alla volta. Serve per `create index concurrently`,
// che Postgres non accetta dentro una transazione: su tabelle grandi (elements,
// activities, element_values) un indice normale bloccherebbe le scritture per minuti.
//
// Regole per i file con la direttiva:
//   - ogni istruzione deve essere idempotente (`if not exists`, `if exists`), perché se
//     una fallisce quelle prima restano applicate e al riavvio il file riparte da capo;
//   - niente `begin`/`commit`: per le parti che vanno insieme si usa un file normale;
//   - un `create index concurrently` fallito lascia un indice INVALID: il migratore lo
//     elimina (anche lui senza blocco) e lo ricrea al tentativo successivo;
//   - su una tabella partizionata (activities, audit_log, metric_points) Postgres non
//     accetta `concurrently`: il migratore crea l'indice sulla sola tabella madre
//     (`on only`), lo costruisce senza blocco su ogni partizione e ve lo aggancia
//     (`alter index … attach partition`). Le partizioni create dopo lo ereditano da sole.
const NoTransaction = "-- regia:no-transaction"

// Migrate applica in ordine i file *.sql di fsys non ancora applicati, ognuno nella
// propria transazione (o fuori, con la direttiva NoTransaction). Un file già applicato
// che risulta modificato blocca l'avvio.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, log *slog.Logger) (applied int, err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "select pg_advisory_lock($1)", migrationLock); err != nil {
		return 0, err
	}
	defer conn.Exec(context.WithoutCancel(ctx), "select pg_advisory_unlock($1)", migrationLock) //nolint:errcheck

	if _, err := conn.Exec(ctx, `create table if not exists schema_migrations (
		version text primary key,
		checksum bytea not null,
		applied_at timestamptz not null default now())`); err != nil {
		return 0, err
	}

	done, err := appliedMigrations(ctx, conn.Conn())
	if err != nil {
		return 0, err
	}
	files, err := readMigrations(fsys)
	if err != nil {
		return 0, err
	}
	for _, m := range files {
		if prev, ok := done[m.version]; ok {
			if !bytes.Equal(prev, m.sum[:]) {
				return applied, fmt.Errorf("la migrazione %s è stata modificata dopo essere stata applicata: crea una nuova migrazione", m.name)
			}
			continue
		}
		if m.noTx {
			log.Info("applico la migrazione fuori transazione", "versione", m.version)
			err = applyNoTx(ctx, conn.Conn(), m, log)
		} else {
			log.Info("applico la migrazione", "versione", m.version)
			err = applyTx(ctx, conn.Conn(), m)
		}
		if err != nil {
			return applied, fmt.Errorf("migrazione %s: %w", m.name, err)
		}
		applied++
	}
	return applied, nil
}

type migration struct {
	name, version string
	body          []byte
	sum           [32]byte
	noTx          bool
}

func readMigrations(fsys fs.FS) ([]migration, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	out := make([]migration, 0, len(names))
	for _, name := range names {
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{
			name: name, version: strings.TrimSuffix(name, ".sql"), body: body,
			sum: sha256.Sum256(body), noTx: hasNoTxDirective(body),
		})
	}
	return out, nil
}

// hasNoTxDirective cerca la direttiva tra le righe di commento iniziali del file.
func hasNoTxDirective(body []byte) bool {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "--") {
			return false
		}
		if line == NoTransaction {
			return true
		}
	}
	return false
}

func applyTx(ctx context.Context, conn *pgx.Conn, m migration) error {
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		// Le migrazioni possono durare più del limite delle richieste (indici, popolamenti).
		if _, err := tx.Exec(ctx, "set local statement_timeout = 0"); err != nil {
			return err
		}
		// Senza argomenti pgx usa il protocollo semplice: il file può contenere più istruzioni.
		if _, err := tx.Exec(ctx, string(m.body)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "insert into schema_migrations (version, checksum) values ($1, $2)", m.version, m.sum[:])
		return err
	})
}

// concurrentIndex riconosce `create [unique] index concurrently [if not exists] nome on [only] tabella …`.
var concurrentIndex = regexp.MustCompile(`(?is)^\s*create\s+(unique\s+)?index\s+concurrently\s+(?:if\s+not\s+exists\s+)?("?[\w.]+"?)\s+on\s+(?:only\s+)?("?[\w.]+"?)\s+(.*)$`)

// applyNoTx esegue le istruzioni una alla volta, ognuna in autocommit: in un'unica
// stringa del protocollo semplice Postgres le metterebbe in una transazione implicita.
func applyNoTx(ctx context.Context, conn *pgx.Conn, m migration, log *slog.Logger) error {
	stmts, err := SplitStatements(string(m.body))
	if err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "set statement_timeout = 0"); err != nil {
		return err
	}
	// Il pool riusa la connessione: il limite delle richieste torna com'era.
	defer conn.Exec(context.WithoutCancel(ctx), "reset statement_timeout") //nolint:errcheck
	for i, st := range stmts {
		if mm := concurrentIndex.FindStringSubmatch(stripLeadingComments(st)); mm != nil {
			err = concurrentlyIndex(ctx, conn, mm[1] != "", mm[2], mm[3], mm[4], st, log)
		} else {
			_, err = conn.Exec(ctx, st)
		}
		if err != nil {
			return fmt.Errorf("istruzione %d di %d: %w", i+1, len(stmts), err)
		}
	}
	_, err = conn.Exec(ctx, "insert into schema_migrations (version, checksum) values ($1, $2)", m.version, m.sum[:])
	return err
}

// concurrentlyIndex crea un indice senza bloccare le scritture, anche su una tabella partizionata.
func concurrentlyIndex(ctx context.Context, conn *pgx.Conn, unique bool, name, table, rest, stmt string, log *slog.Logger) error {
	var kind string
	if err := conn.QueryRow(ctx, "select coalesce((select relkind::text from pg_class where oid = to_regclass($1)), '')", table).Scan(&kind); err != nil {
		return err
	}
	if kind != "p" {
		if err := dropInvalidIndex(ctx, conn, name, log); err != nil {
			return err
		}
		_, err := conn.Exec(ctx, stmt)
		return err
	}
	uq := ""
	if unique {
		uq = "unique "
	}
	// Sulla madre l'indice nasce non valido e lo diventa quando tutte le partizioni hanno il loro.
	if _, err := conn.Exec(ctx, fmt.Sprintf("create %sindex if not exists %s on only %s %s", uq, name, table, rest)); err != nil {
		return err
	}
	parts, err := partitions(ctx, conn, table, name)
	if err != nil {
		return err
	}
	for _, p := range parts {
		if p.attached {
			continue
		}
		child := childIndexName(p.name, name)
		if err := dropInvalidIndex(ctx, conn, child, log); err != nil {
			return err
		}
		log.Info("indice sulla partizione", "partizione", p.name, "indice", child)
		if _, err := conn.Exec(ctx, fmt.Sprintf("create %sindex concurrently if not exists %s on %s %s", uq, child, p.name, rest)); err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, fmt.Sprintf("alter index %s attach partition %s", name, child)); err != nil {
			return err
		}
	}
	return nil
}

type partition struct {
	name     string
	attached bool // ha già un indice agganciato a quello della madre
}

// partitions elenca le partizioni dirette della tabella e dice quali hanno già l'indice.
func partitions(ctx context.Context, conn *pgx.Conn, table, index string) ([]partition, error) {
	rows, err := conn.Query(ctx, `
		select c.oid::regclass::text,
		       exists (select 1 from pg_inherits ii join pg_index x on x.indexrelid = ii.inhrelid
		                where ii.inhparent = to_regclass($2) and x.indrelid = c.oid)
		  from pg_inherits i join pg_class c on c.oid = i.inhrelid
		 where i.inhparent = to_regclass($1)
		 order by 1`, table, index)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (partition, error) {
		var p partition
		return p, r.Scan(&p.name, &p.attached)
	})
}

// childIndexName è il nome dell'indice sulla partizione, entro i 63 byte di Postgres.
func childIndexName(partition, index string) string {
	strip := func(s string) string {
		s = strings.Trim(s, `"`)
		if i := strings.LastIndexByte(s, '.'); i >= 0 {
			s = s[i+1:]
		}
		return s
	}
	n := strip(index) + "_" + strip(partition)
	if len(n) > 63 {
		h := sha256.Sum256([]byte(n))
		n = n[:54] + "_" + fmt.Sprintf("%x", h[:4])
	}
	return n
}

// dropInvalidIndex elimina l'indice rimasto INVALID da un `create index concurrently`
// interrotto: con `if not exists` non verrebbe mai ricostruito. Gli indici delle tabelle
// partizionate (relkind I) restano: sono non validi finché mancano partizioni.
func dropInvalidIndex(ctx context.Context, conn *pgx.Conn, name string, log *slog.Logger) error {
	var invalid bool
	err := conn.QueryRow(ctx, `select exists (select 1 from pg_index i join pg_class c on c.oid = i.indexrelid
		where i.indexrelid = to_regclass($1) and not i.indisvalid and c.relkind = 'i')`, name).Scan(&invalid)
	if err != nil || !invalid {
		return err
	}
	log.Warn("indice non valido lasciato da un tentativo precedente: lo ricreo", "indice", name)
	_, err = conn.Exec(ctx, "drop index concurrently if exists "+name)
	return err
}

func appliedMigrations(ctx context.Context, conn *pgx.Conn) (map[string][]byte, error) {
	done := map[string][]byte{}
	rows, err := conn.Query(ctx, "select version, checksum from schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		var c []byte
		if err := rows.Scan(&v, &c); err != nil {
			return nil, err
		}
		done[v] = c
	}
	return done, rows.Err()
}

// MigrationStatus confronta le migrazioni del binario con quelle del database.
type MigrationStatus struct {
	Applied  []string // nel database e nel binario, con lo stesso checksum
	Pending  []string // nel binario, da applicare
	Modified []string // applicate ma con un checksum diverso: l'avvio si bloccherebbe
	Unknown  []string // nel database ma non nel binario: il database è di una versione più recente
}

// Fresh dice se il database non ha ancora nessuna migrazione (prima installazione).
func (s MigrationStatus) Fresh() bool {
	return len(s.Applied) == 0 && len(s.Modified) == 0 && len(s.Unknown) == 0
}

// Status legge lo stato delle migrazioni senza applicarle e senza scrivere nulla.
func Status(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) (MigrationStatus, error) {
	var st MigrationStatus
	files, err := readMigrations(fsys)
	if err != nil {
		return st, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return st, err
	}
	defer conn.Release()
	var exists bool
	if err := conn.QueryRow(ctx, "select to_regclass('schema_migrations') is not null").Scan(&exists); err != nil {
		return st, err
	}
	done := map[string][]byte{}
	if exists {
		if done, err = appliedMigrations(ctx, conn.Conn()); err != nil {
			return st, err
		}
	}
	known := map[string]bool{}
	for _, m := range files {
		known[m.version] = true
		prev, ok := done[m.version]
		switch {
		case !ok:
			st.Pending = append(st.Pending, m.version)
		case !bytes.Equal(prev, m.sum[:]):
			st.Modified = append(st.Modified, m.version)
		default:
			st.Applied = append(st.Applied, m.version)
		}
	}
	for v := range done {
		if !known[v] {
			st.Unknown = append(st.Unknown, v)
		}
	}
	sort.Strings(st.Unknown)
	return st, nil
}

// Versions elenca le versioni delle migrazioni incluse in fsys, in ordine.
func Versions(fsys fs.FS) ([]string, error) {
	files, err := readMigrations(fsys)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(files))
	for i, m := range files {
		out[i] = m.version
	}
	return out, nil
}

// SplitStatements divide un file SQL nelle sue istruzioni, rispettando stringhe,
// identificatori tra virgolette, blocchi $tag$ … $tag$ e commenti. Le istruzioni
// vuote o fatte solo di commenti si scartano.
func SplitStatements(sql string) ([]string, error) {
	var out []string
	start := 0
	flush := func(end int) {
		st := strings.TrimSpace(sql[start:end])
		if st != "" && !onlyComments(st) {
			out = append(out, st)
		}
		start = end + 1
	}
	for i := 0; i < len(sql); i++ {
		switch c := sql[i]; {
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			depth := 1
			for i += 2; i < len(sql) && depth > 0; i++ {
				switch {
				case sql[i] == '/' && i+1 < len(sql) && sql[i+1] == '*':
					depth++
					i++
				case sql[i] == '*' && i+1 < len(sql) && sql[i+1] == '/':
					depth--
					i++
				}
			}
			if depth > 0 {
				return nil, fmt.Errorf("commento /* non chiuso")
			}
			i--
		case c == '\'' || c == '"':
			j := i + 1
			for ; j < len(sql); j++ {
				if sql[j] == c {
					if j+1 < len(sql) && sql[j+1] == c { // '' o "" raddoppiati
						j++
						continue
					}
					break
				}
			}
			if j >= len(sql) {
				return nil, fmt.Errorf("stringa %c non chiusa", c)
			}
			i = j
		case c == '$':
			if tag := dollarTag(sql[i:]); tag != "" {
				end := strings.Index(sql[i+len(tag):], tag)
				if end < 0 {
					return nil, fmt.Errorf("blocco %s non chiuso", tag)
				}
				i += len(tag) + end + len(tag) - 1
			}
		case c == ';':
			flush(i)
		}
	}
	if start < len(sql) {
		flush(len(sql))
	}
	return out, nil
}

var dollarTagRe = regexp.MustCompile(`^\$[A-Za-z_]?[A-Za-z0-9_]*\$`)

func dollarTag(s string) string { return dollarTagRe.FindString(s) }

func stripLeadingComments(st string) string {
	for {
		st = strings.TrimSpace(st)
		if !strings.HasPrefix(st, "--") {
			return st
		}
		nl := strings.IndexByte(st, '\n')
		if nl < 0 {
			return ""
		}
		st = st[nl+1:]
	}
}

func onlyComments(st string) bool {
	for _, line := range strings.Split(st, "\n") {
		if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "--") {
			return false
		}
	}
	return true
}
