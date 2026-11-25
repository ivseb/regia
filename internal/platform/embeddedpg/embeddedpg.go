// Package embeddedpg gestisce il Postgres incluso nel pacchetto: initdb al primo
// avvio, processo figlio sul solo socket unix (nessuna porta TCP), arresto pulito.
package embeddedpg

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	// Nomi storici, dal nome di lavoro del progetto (myTrack): stanno nei dati delle istanze
	// esistenti e nelle migrazioni già applicate, quindi non cambiano col nome del prodotto.
	// Superuser è il proprietario del cluster e dello schema (ruolo di piattaforma).
	Superuser = "mytrack_owner"
	// AppRole è il ruolo usato dalle richieste: non è proprietario delle tabelle
	// e non ha BYPASSRLS, quindi la Row Level Security vale sempre.
	AppRole  = "mytrack_app"
	Database = "mytrack"
	port     = 5432
	// MinMajor è la versione minima accettata; quella del pacchetto è 18.
	MinMajor         = 16
	RecommendedMajor = 18
)

type Server struct {
	pgData  string
	binDir  string
	sockDir string
	log     *slog.Logger
	major   int

	// attached: Postgres era già avviato da un altro processo regia sulla stessa
	// cartella dati (es. "regia tenant create" mentre gira "serve"): lo si usa e non lo si ferma.
	attached bool
	cmd      *exec.Cmd
	exited   chan struct{} // chiuso quando il processo termina
	exitErr  error
}

// New prepara (senza avviare) il Postgres incluso nella cartella dataDir/pg.
func New(dataDir, binDir string, log *slog.Logger) (*Server, error) {
	bin, err := findBinDir(binDir)
	if err != nil {
		return nil, err
	}
	s := &Server{
		pgData: filepath.Join(dataDir, "pg"),
		binDir: bin,
		log:    log.With("component", "embeddedpg"),
	}
	s.sockDir, err = socketDir(dataDir)
	if err != nil {
		return nil, err
	}
	s.major, err = s.version()
	if err != nil {
		return nil, err
	}
	if s.major < MinMajor {
		return nil, fmt.Errorf("Postgres %d non supportato: serve almeno la %d (il pacchetto include la %d)", s.major, MinMajor, RecommendedMajor)
	}
	if s.major < RecommendedMajor {
		s.log.Warn("versione di Postgres inferiore a quella del pacchetto", "trovata", s.major, "consigliata", RecommendedMajor)
	}
	return s, nil
}

// OwnerURL è la connessione del ruolo di piattaforma (migrazioni, job di piattaforma).
func (s *Server) OwnerURL() string { return s.url(Superuser) }

// AppURL è la connessione del ruolo applicativo.
func (s *Server) AppURL() string { return s.url(AppRole) }

func (s *Server) url(user string) string {
	return fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=disable", s.sockDir, port, user, Database)
}

// BinDir è la cartella dei binari di Postgres in uso (lì si cercano anche pg_dump e pg_restore).
func (s *Server) BinDir() string { return s.binDir }

// Major è la versione principale dei binari di Postgres.
func (s *Server) Major() int { return s.major }

// Attached dice se Start si è agganciato a un Postgres avviato da un altro processo.
func (s *Server) Attached() bool { return s.attached }

// Initialized dice se la cartella dati contiene già un cluster (initdb fatto).
func (s *Server) Initialized() bool {
	_, err := os.Stat(filepath.Join(s.pgData, "PG_VERSION"))
	return err == nil
}

// Running dice se un Postgres risponde già sul socket di questa cartella dati.
func (s *Server) Running(ctx context.Context) bool { return s.running(ctx) }

// DataMajor legge la versione principale del cluster in dataDir/pg (file PG_VERSION):
// 0 se la cartella non è inizializzata.
func DataMajor(dataDir string) (int, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, "pg", "PG_VERSION"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// Env è l'ambiente da usare per i programmi di Postgres (pg_dump, pg_restore) sul socket incluso.
func Env() []string { return pgEnv() }

// Start inizializza il cluster se serve, avvia Postgres e attende che accetti connessioni.
func (s *Server) Start(ctx context.Context) error {
	if s.running(ctx) {
		s.attached = true
		s.log.Info("Postgres incluso già avviato da un altro processo: mi collego", "socket", s.sockDir)
		return s.ensureDatabase(ctx)
	}
	if err := s.initdb(ctx); err != nil {
		return err
	}
	args := []string{
		"-D", s.pgData,
		"-k", s.sockDir,
		"-p", strconv.Itoa(port),
		"-c", "listen_addresses=", // solo socket unix
		"-c", "unix_socket_permissions=0700",
		"-c", "timezone=UTC",
		"-c", "log_timezone=UTC",
		"-c", "max_connections=100",
		"-c", "shared_buffers=128MB",
		"-c", "log_min_messages=warning",
	}
	cmd := exec.Command(filepath.Join(s.binDir, "postgres"), args...)
	// Gruppo di processi separato: il Ctrl-C del terminale non arriva a Postgres,
	// che viene fermato da noi dopo aver chiuso il server HTTP.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	cmd.Stdout = io.Discard
	cmd.Env = pgEnv()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("avvio di postgres: %w", err)
	}
	s.cmd = cmd
	s.exited = make(chan struct{})
	go s.forwardLogs(stderr)
	go func() { s.exitErr = cmd.Wait(); close(s.exited) }()

	if err := s.waitReady(ctx, 30*time.Second); err != nil {
		_ = s.Stop(5 * time.Second)
		return err
	}
	if err := s.ensureDatabase(ctx); err != nil {
		_ = s.Stop(5 * time.Second)
		return err
	}
	s.log.Info("Postgres incluso avviato", "versione", s.major, "socket", s.sockDir, "dati", s.pgData)
	return nil
}

// Stop chiede un arresto veloce (SIGINT); se non basta, uno immediato (SIGQUIT).
func (s *Server) Stop(timeout time.Duration) error {
	if s.attached || s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	select {
	case <-s.exited:
		return nil // già terminato
	default:
	}
	_ = s.cmd.Process.Signal(syscall.SIGINT)
	select {
	case <-s.exited:
		s.log.Info("Postgres incluso arrestato")
		return nil
	case <-time.After(timeout):
		_ = s.cmd.Process.Signal(syscall.SIGQUIT)
		<-s.exited
		return errors.New("Postgres arrestato in modo immediato: al prossimo avvio farà il recupero")
	}
}

// running dice se un Postgres risponde già sul socket di questa cartella dati.
func (s *Server) running(ctx context.Context) bool {
	if _, err := os.Stat(filepath.Join(s.pgData, "postmaster.pid")); err != nil {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := pgx.Connect(cctx, fmt.Sprintf("host=%s port=%d user=%s dbname=postgres sslmode=disable", s.sockDir, port, Superuser))
	if err != nil {
		return false
	}
	_ = conn.Close(ctx)
	return true
}

func (s *Server) initdb(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(s.pgData, "PG_VERSION")); err == nil {
		return nil
	}
	if err := os.MkdirAll(s.pgData, 0o700); err != nil {
		return err
	}
	s.log.Info("prima inizializzazione del database", "dati", s.pgData)
	cmd := exec.CommandContext(ctx, filepath.Join(s.binDir, "initdb"),
		"-D", s.pgData,
		"-U", Superuser,
		"--auth=trust", // accesso solo dal socket unix con permessi 0700
		"--encoding=UTF8",
		"--no-locale",
		"--no-instructions",
	)
	cmd.Env = pgEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("initdb: %w\n%s", err, out)
	}
	return nil
}

func (s *Server) waitReady(ctx context.Context, max time.Duration) error {
	deadline := time.Now().Add(max)
	adminURL := fmt.Sprintf("host=%s port=%d user=%s dbname=postgres sslmode=disable", s.sockDir, port, Superuser)
	for {
		select {
		case <-s.exited:
			return fmt.Errorf("postgres si è chiuso durante l'avvio: %v (vedi i log sopra)", s.exitErr)
		default:
		}
		cctx, cancel := context.WithTimeout(ctx, time.Second)
		conn, err := pgx.Connect(cctx, adminURL)
		cancel()
		if err == nil {
			_ = conn.Close(ctx)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("postgres non risponde dopo %s: %w", max, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Server) ensureDatabase(ctx context.Context) error {
	adminURL := fmt.Sprintf("host=%s port=%d user=%s dbname=postgres sslmode=disable", s.sockDir, port, Superuser)
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, "select exists (select 1 from pg_database where datname = $1)", Database).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = conn.Exec(ctx, "create database "+Database)
	return err
}

func (s *Server) version() (int, error) {
	out, err := exec.Command(filepath.Join(s.binDir, "postgres"), "--version").Output()
	if err != nil {
		return 0, fmt.Errorf("postgres --version: %w", err)
	}
	m := regexp.MustCompile(`(\d+)(\.\d+)?`).FindStringSubmatch(string(out))
	if m == nil {
		return 0, fmt.Errorf("versione di postgres non riconosciuta: %q", out)
	}
	return strconv.Atoi(m[1])
}

func (s *Server) forwardLogs(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.Contains(line, "PANIC:"), strings.Contains(line, "FATAL:"), strings.Contains(line, "ERROR:"):
			s.log.Error(line)
		case strings.Contains(line, "WARNING:"):
			s.log.Warn(line)
		default:
			s.log.Debug(line)
		}
	}
}

// pgEnv è l'ambiente dei processi Postgres. Su macOS un locale non valido nell'ambiente
// fa fallire l'avvio ("postmaster became multithreaded"): si fissa il locale C, coerente
// con initdb --no-locale (ordinamenti e confronti sono byte per byte e prevedibili).
func pgEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "LC_") || strings.HasPrefix(e, "LANG=") || strings.HasPrefix(e, "PG") {
			continue
		}
		env = append(env, e)
	}
	return append(env, "LC_ALL=C", "LANG=C")
}

// findBinDir cerca initdb e postgres: cartella indicata, poi pgsql/bin accanto
// all'eseguibile (layout del pacchetto), poi il PATH.
func findBinDir(explicit string) (string, error) {
	var candidates []string
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "pgsql", "bin"))
	}
	if p, err := exec.LookPath("initdb"); err == nil {
		candidates = append(candidates, filepath.Dir(p))
	}
	for _, d := range candidates {
		if isExec(filepath.Join(d, "initdb")) && isExec(filepath.Join(d, "postgres")) {
			return d, nil
		}
	}
	return "", errors.New("binari di Postgres non trovati: indica la cartella con --pg-bin o REGIA_PG_BIN, oppure imposta DATABASE_URL per un Postgres esterno")
}

func isExec(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// socketDir sceglie la cartella del socket. Il percorso di un socket unix ha un
// limite di circa 100 byte: se dataDir è troppo lunga si usa /tmp/regia-<hash>.
func socketDir(dataDir string) (string, error) {
	dir := filepath.Join(dataDir, "run")
	if len(dir)+len("/.s.PGSQL.5432") > 100 {
		h := sha256.Sum256([]byte(dataDir))
		dir = filepath.Join("/tmp", "regia-"+hex.EncodeToString(h[:6]))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}
