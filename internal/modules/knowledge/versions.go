package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Versioni delle pagine (page_versions): ogni salvataggio è una versione con autore, data e
// nota; una versione approvata porta anche chi l'ha approvata e quando. Il confronto è a
// righe con il dettaglio a parole; il ripristino crea una versione nuova uguale a quella
// scelta (la storia non si riscrive).

// VersionInfo è una riga dello storico.
type VersionInfo struct {
	Version    int        `json:"version"`
	Title      string     `json:"title"`
	Note       string     `json:"note"`
	AuthorID   *string    `json:"author_id"`
	Author     string     `json:"author"`
	CreatedAt  time.Time  `json:"created_at"`
	ApprovedAt *time.Time `json:"approved_at"`
	ApprovedBy string     `json:"approved_by"`
	Size       int        `json:"size"`
	Current    bool       `json:"current"`
	InForce    bool       `json:"in_force"` // versione approvata in vigore (documenti controllati)
}

// VersionView è una versione con il testo.
type VersionView struct {
	VersionInfo
	Body string `json:"body"`
}

const versionColumns = `v.version, v.title, v.note, v.author_id::text, coalesce(a.display_name, ''), v.created_at, v.approved_at,
	coalesce(p.display_name, ''), length(coalesce(v.body->>'md', ''))`

const versionFrom = ` from page_versions v
	left join members a on a.tenant_id = v.tenant_id and a.id = v.author_id
	left join members p on p.tenant_id = v.tenant_id and p.id = v.approved_by`

func (k *kbRow) mark(v *VersionInfo) {
	v.Current = v.Version == k.version
	v.InForce = k.approvedVersion != nil && *k.approvedVersion == v.Version
}

// Versions elenca lo storico, dalla più recente.
func (s *Service) Versions(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) ([]VersionInfo, error) {
	el, _, k, err := s.loadPage(ctx, tx, actor, ref, false)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select `+versionColumns+versionFrom+` where v.element_id = $1 order by v.version desc limit 1000`, el.ID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (VersionInfo, error) {
		var v VersionInfo
		err := r.Scan(&v.Version, &v.Title, &v.Note, &v.AuthorID, &v.Author, &v.CreatedAt, &v.ApprovedAt, &v.ApprovedBy, &v.Size)
		k.mark(&v)
		return v, err
	})
	if out == nil {
		out = []VersionInfo{}
	}
	return out, err
}

func (s *Service) version(ctx context.Context, tx pgx.Tx, pageID string, n int, k *kbRow) (*VersionView, error) {
	var v VersionView
	var raw []byte
	err := tx.QueryRow(ctx, `select `+versionColumns+`, v.body`+versionFrom+` where v.element_id = $1 and v.version = $2`, pageID, n).
		Scan(&v.Version, &v.Title, &v.Note, &v.AuthorID, &v.Author, &v.CreatedAt, &v.ApprovedAt, &v.ApprovedBy, &v.Size, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	v.Body = readBody(raw).MD
	k.mark(&v.VersionInfo)
	return &v, nil
}

// Version restituisce una versione con il testo.
func (s *Service) Version(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, n int) (*VersionView, error) {
	el, _, k, err := s.loadPage(ctx, tx, actor, ref, false)
	if err != nil {
		return nil, err
	}
	return s.version(ctx, tx, el.ID, n, k)
}

// Comparison è il confronto tra due versioni.
type Comparison struct {
	From         VersionInfo `json:"from"`
	To           VersionInfo `json:"to"`
	TitleChanged bool        `json:"title_changed"`
	Lines        []DiffLine  `json:"lines"`
	Stats        DiffStats   `json:"stats"`
}

// Compare confronta la versione from con la versione to (to = 0: la corrente).
func (s *Service) Compare(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, from, to int) (*Comparison, error) {
	el, _, k, err := s.loadPage(ctx, tx, actor, ref, false)
	if err != nil {
		return nil, err
	}
	if to == 0 {
		to = k.version
	}
	if from == 0 {
		from = max(to-1, 1)
	}
	a, err := s.version(ctx, tx, el.ID, from, k)
	if err != nil {
		return nil, err
	}
	b, err := s.version(ctx, tx, el.ID, to, k)
	if err != nil {
		return nil, err
	}
	lines, st := Diff(a.Body, b.Body)
	return &Comparison{From: a.VersionInfo, To: b.VersionInfo, TitleChanged: a.Title != b.Title, Lines: lines, Stats: st}, nil
}

// Restore crea una nuova versione uguale alla versione n (titolo e testo).
func (s *Service) Restore(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, n int, note string) (*PageView, error) {
	el, sp, k, err := s.loadPage(ctx, tx, actor, ref, true)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Write {
		return nil, engine.ErrForbidden
	}
	if n == k.version {
		return nil, engine.InvalidCode("validation.knowledge.version_current", "version", "è già la versione corrente")
	}
	v, err := s.version(ctx, tx, el.ID, n, k)
	if err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("Ripristino della versione %d", n)
	if note = strings.TrimSpace(note); note != "" {
		msg += ": " + note
	}
	if err := s.save(ctx, tx, actor, el, k, SaveInput{Title: &v.Title, Body: v.Body, Note: msg, BaseVersion: k.version}, "restored"); err != nil {
		return nil, err
	}
	return s.GetPage(ctx, tx, actor, el.ID)
}
