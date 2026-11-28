package contract

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Commit è un commit letto da un connettore (o una pull request unita, con il suo commit di
// merge) che nel messaggio, nel titolo o nel nome del branch cita la chiave di un elemento.
type Commit struct {
	Service  Service   `json:"service"`
	SHA      string    `json:"sha"`
	Message  string    `json:"message"` // messaggio intero (per le PR: il titolo)
	Branch   string    `json:"branch,omitempty"`
	Author   string    `json:"author,omitempty"` // nome o login, mai l'email
	At       time.Time `json:"at"`
	URL      string    `json:"url,omitempty"`
	Source   string    `json:"source,omitempty"` // commit | pull_request
	PRNumber int       `json:"pr_number,omitempty"`
}

// CommitIngestor salva i commit legati agli elementi. Lo implementa il modulo devsecops;
// un Ingestor che non lo implementa semplicemente non li conserva. Idempotente: un commit
// già visto non crea righe nuove. Restituisce quanti collegamenti nuovi ha creato.
type CommitIngestor interface {
	IngestCommits(ctx context.Context, tx pgx.Tx, actor engine.Actor, svc Service, commits []Commit) (int, error)
}

// DoraRecomputer ricalcola le metriche DORA del tenant (primo ricalcolo dopo la prima
// sincronizzazione di un connettore). Lo implementa il modulo devsecops.
type DoraRecomputer interface {
	// RecomputeDoraIfEmpty ricalcola solo se non c'è ancora nessuna metrica; dice se l'ha fatto.
	RecomputeDoraIfEmpty(ctx context.Context, tx pgx.Tx) (bool, error)
}

var commitKeyRe = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,19})-([0-9]+)\b`)

// ElementRef è una chiave di elemento riconosciuta in un testo (spazio e numero).
type ElementRef struct {
	Space  string
	Number int64
}

func (r ElementRef) String() string { return r.Space + "-" + strconv.FormatInt(r.Number, 10) }

// CommitKeys riconosce le chiavi di elemento (HL-12) nei testi indicati: messaggio intero di
// un commit, oppure titolo e nome del branch di una pull request. Ogni chiave una volta sola,
// nell'ordine in cui compare. È solo sintassi: se lo spazio e l'elemento esistano lo decide
// chi salva (IngestCommits).
func CommitKeys(texts ...string) []ElementRef {
	var out []ElementRef
	seen := map[ElementRef]bool{}
	for _, t := range texts {
		// Il trattino basso è «parola» per \b: nei nomi di branch (HL-12_fix) separa come uno spazio.
		for _, m := range commitKeyRe.FindAllStringSubmatch(strings.ReplaceAll(t, "_", " "), -1) {
			n, err := strconv.ParseInt(m[2], 10, 64)
			if err != nil || n < 1 {
				continue
			}
			ref := ElementRef{Space: m[1], Number: n}
			if !seen[ref] {
				seen[ref] = true
				out = append(out, ref)
			}
		}
	}
	return out
}

// CommitTitle è la prima riga del messaggio, troncata.
func CommitTitle(msg string, max int) string {
	line, _, _ := strings.Cut(strings.TrimSpace(msg), "\n")
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return line
}

// closeKeyRe: «fixes HL-12», «Closes: HL-12», «resolved HL-12». La parola chiave deve
// precedere la chiave, separata da spazi (o da due punti); «fix(HL-12):» non chiude.
var closeKeyRe = regexp.MustCompile(`(?i)\b(fix|fixes|fixed|close|closes|closed|resolve|resolves|resolved)\b:?[ \t]+([A-Z][A-Z0-9]{1,19})-([0-9]+)\b`)

// CloseKey è una chiave citata con una parola chiave di chiusura.
type CloseKey struct {
	Ref     ElementRef
	Keyword string // in minuscolo, come scritta
}

// CloseKeys riconosce le chiavi precedute da una parola chiave di chiusura (fixes, closes,
// resolves e varianti). Lo spazio della chiave va in maiuscolo, come in CommitKeys.
func CloseKeys(text string) []CloseKey {
	var out []CloseKey
	seen := map[ElementRef]bool{}
	for _, m := range closeKeyRe.FindAllStringSubmatch(strings.ReplaceAll(text, "_", " "), -1) {
		n, err := strconv.ParseInt(m[3], 10, 64)
		if err != nil || n < 1 || m[2] != strings.ToUpper(m[2]) {
			continue
		}
		ref := ElementRef{Space: m[2], Number: n}
		if !seen[ref] {
			seen[ref] = true
			out = append(out, CloseKey{Ref: ref, Keyword: strings.ToLower(m[1])})
		}
	}
	return out
}
