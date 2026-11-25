package grc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Evidenze: elementi "evidence" dello spazio GRC con ext_evidence. Manuali (allegato, link,
// pagina Knowledge, nota), create dal responsabile del controllo o dalla compliance e
// validate da un'altra persona con grc.admin, che le firma; oppure automatiche, inviate da
// una pipeline o da un collettore (POST /api/v1/grc/evidence/automatic, RecordAutomatic),
// già valide e firmate a nome del collettore. Un'evidenza vale fino a valid_until: di
// default la durata della frequenza del controllo più frequente a cui è collegata.
// Le verifiche dei requisiti di progetto collegati a requisiti normativi valgono come
// evidenze senza essere copiate qui (coverage.go, loadEvidence).

var manualKinds = []string{"attachment", "link", "knowledge_page", "note"}

// KindNamesEvidence sono i nomi italiani dei tipi di evidenza.
var KindNamesEvidence = map[string]string{"attachment": "allegato", "link": "link", "knowledge_page": "pagina Knowledge", "note": "nota",
	"release": "rilascio", "automatic": "automatica", CollectorVerification: "verifica di un requisito di progetto"}

// EvidenceInput crea un'evidenza manuale.
type EvidenceInput struct {
	Controls   []string `json:"controls"` // chiavi dei controlli (GRC-3): l'evidenza vale per tutti
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	Note       string   `json:"note"`
	URL        string   `json:"url"`
	Page       string   `json:"page"`   // chiave della pagina Knowledge (o di un altro elemento)
	Scope      string   `json:"scope"`  // tenant (default) | legal_entity | application | process
	Target     string   `json:"target"` // chiave o codice del perimetro
	ValidFrom  string   `json:"valid_from"`
	ValidUntil string   `json:"valid_until"`
}

func parseDay(field, s string) (*time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.DateOnly, strings.TrimSpace(s))
	if err != nil {
		return nil, engine.InvalidCode("validation.date_invalid", field, "data non valida (AAAA-MM-GG)")
	}
	return &t, nil
}

func checkURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(s) > 2000 {
		return engine.InvalidValue("url", "indirizzo non valido (http o https)")
	}
	return nil
}

// isOwnerOf dice se l'attore è responsabile di tutti i controlli indicati.
func isOwnerOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, controlIDs []string) (bool, error) {
	if actor.MemberID == "" || len(controlIDs) == 0 {
		return false, nil
	}
	var n int
	err := tx.QueryRow(ctx, "select count(*) from ext_control where element_id = any($1) and owner_id = $2", controlIDs, actor.MemberID).Scan(&n)
	return n == len(controlIDs), err
}

// CreateEvidence crea un'evidenza manuale in bozza, collegata ai controlli.
func (s *Service) CreateEvidence(ctx context.Context, tx pgx.Tx, actor engine.Actor, in EvidenceInput) (*EvidenceView, error) {
	if len(in.Controls) == 0 {
		return nil, engine.Required("controls", "indica almeno un controllo")
	}
	ids := []string{}
	for _, k := range in.Controls {
		id, err := s.controlRef(ctx, tx, k)
		if err != nil {
			return nil, engine.NotFound("controls", "control", "controllo %q inesistente", k)
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if !actor.Has(PermAdmin) {
		ok, err := isOwnerOf(ctx, tx, actor, ids)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, engine.ErrForbidden
		}
	}
	if in.Kind == "" {
		in.Kind = "note"
	}
	if !slices.Contains(manualKinds, in.Kind) {
		return nil, engine.InvalidValue("kind", "tipo non ammesso (%s)", strings.Join(manualKinds, ", "))
	}
	in.URL = strings.TrimSpace(in.URL)
	if in.Kind == "link" && in.URL == "" {
		return nil, engine.InvalidCode("validation.grc.link_needs_url", "url", "per un link serve l'indirizzo")
	}
	if in.URL != "" {
		if err := checkURL(in.URL); err != nil {
			return nil, err
		}
	}
	var pageID any
	if in.Kind == "knowledge_page" {
		if strings.TrimSpace(in.Page) == "" {
			return nil, engine.Required("page", "indica la pagina")
		}
		pg, err := s.eng.Get(ctx, tx, actor, in.Page)
		if err != nil {
			return nil, engine.NotFound("page", "page", "pagina %q inesistente", in.Page)
		}
		pageID = pg.ID
	}
	if len(in.Note) > 20000 {
		return nil, engine.InvalidCode("validation.note_too_long", "note", "nota troppo lunga")
	}
	if in.Kind == "note" && strings.TrimSpace(in.Note) == "" {
		return nil, engine.Required("note", "scrivi la nota")
	}
	sc, err := s.resolveScope(ctx, tx, in.Scope, in.Target)
	if err != nil {
		return nil, err
	}
	from, err := parseDay("valid_from", in.ValidFrom)
	if err != nil {
		return nil, err
	}
	until, err := parseDay("valid_until", in.ValidUntil)
	if err != nil {
		return nil, err
	}
	validFrom := s.Now()
	if from != nil {
		validFrom = *from
	}
	if until != nil && !until.After(validFrom) {
		return nil, engine.InvalidCode("validation.grc.valid_until_before_start", "valid_until", "la scadenza deve venire dopo l'inizio della validità")
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: EvidenceType, Title: in.Title,
		BodyText: strings.TrimSpace(in.Note)})
	if err != nil {
		return nil, err
	}
	el2, ou := scopeTargets(sc)
	if _, err := tx.Exec(ctx, `insert into ext_evidence (tenant_id, element_id, source, kind, url, page_element_id, scope_kind, target_element_id,
		target_org_unit_id, valid_from, valid_until, status, created_by)
		values (app_tenant(), $1, 'manual', $2, $3, $4, $5, $6, $7, $8, $9, 'draft', $10)`,
		el.ID, in.Kind, nullable(in.URL), pageID, sc.Kind, el2, ou, validFrom, until, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := linkControls(ctx, tx, el.ID, ids); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.evidence.create", EvidenceType, el.ID, map[string]any{"key": el.Key, "kind": in.Kind,
		"controls": ids, "scope": sc.Kind}); err != nil {
		return nil, err
	}
	return s.Evidence(ctx, tx, actor, el.ID)
}

func scopeTargets(sc Scope) (el, ou any) {
	switch sc.Kind {
	case "legal_entity":
		return nil, sc.ID
	case "application", "process":
		return sc.ID, nil
	}
	return nil, nil
}

func linkControls(ctx context.Context, tx pgx.Tx, evidenceID string, controls []string) error {
	_, err := tx.Exec(ctx, `insert into grc_evidence_controls (tenant_id, evidence_element_id, control_element_id)
		select app_tenant(), $1, c from unnest($2::uuid[]) c on conflict do nothing`, evidenceID, controls)
	return err
}

// sigInput è il contenuto firmato di un'evidenza.
type sigInput struct {
	id, kind, title, body, url, page, scopeKind, target string
	attachments                                         []string
	validFrom                                           time.Time
	validUntil                                          *time.Time
	signer                                              string
	at                                                  time.Time
}

// signature è lo SHA-256 del contenuto, di chi lo firma e di quando: cambia se cambia
// qualcosa (anche un allegato tolto), quindi rende visibile ogni modifica successiva.
func (in sigInput) signature() string {
	until := ""
	if in.validUntil != nil {
		until = in.validUntil.UTC().Format(time.RFC3339Nano)
	}
	att := slices.Clone(in.attachments)
	sort.Strings(att)
	h := sha256.New()
	fmt.Fprintf(h, "regia.grc.evidence/v1\nid=%s\nkind=%s\ntitle=%s\nbody=%s\nurl=%s\npage=%s\nscope=%s:%s\nattachments=%s\nfrom=%s\nuntil=%s\nsigner=%s\nat=%s\n",
		in.id, in.kind, in.title, in.body, in.url, in.page, in.scopeKind, in.target, strings.Join(att, ","),
		in.validFrom.UTC().Format(time.RFC3339Nano), until, in.signer, in.at.UTC().Format(time.RFC3339Nano))
	return hex.EncodeToString(h.Sum(nil))
}

// loadSig legge il contenuto firmabile di un'evidenza (allegati attuali compresi).
func loadSig(ctx context.Context, tx pgx.Tx, id string) (*sigInput, *evRow, error) {
	r := &evRow{}
	err := tx.QueryRow(ctx, `select e.id, s.key || '-' || e.number, e.title, e.body_text, x.kind, coalesce(x.url, ''), coalesce(x.page_element_id::text, ''),
		x.scope_kind, coalesce(coalesce(x.target_element_id, x.target_org_unit_id)::text, ''), x.valid_from, x.valid_until, x.status,
		x.source, coalesce(x.collector, ''), coalesce(x.created_by::text, ''), coalesce(x.validated_by::text, ''), x.validated_at, coalesce(x.signature, ''),
		coalesce((select array_agg(encode(a.sha256, 'hex')) from attachments a
		  where a.tenant_id = x.tenant_id and a.element_id = x.element_id and a.removed_at is null), '{}')
		from ext_evidence x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where x.element_id = $1 and e.deleted_at is null`, id).
		Scan(&r.id, &r.key, &r.title, &r.body, &r.kind, &r.url, &r.page, &r.scopeKind, &r.target, &r.validFrom, &r.validUntil, &r.status,
			&r.source, &r.collector, &r.createdBy, &r.validatedBy, &r.validatedAt, &r.signature, &r.attachments)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	in := &sigInput{id: r.id, kind: r.kind, title: r.title, body: r.body, url: r.url, page: r.page, scopeKind: r.scopeKind, target: r.target,
		attachments: r.attachments, validFrom: r.validFrom, validUntil: r.validUntil}
	return in, r, nil
}

type evRow struct {
	id, key, title, body, kind, url, page, scopeKind, target, status, source, collector, createdBy, validatedBy, signature string
	validFrom                                                                                                              time.Time
	validUntil, validatedAt                                                                                                *time.Time
	attachments                                                                                                            []string
}

func (r *evRow) signer() string {
	if r.source == "automatic" {
		return "collector:" + r.collector + ":" + r.validatedBy
	}
	return r.validatedBy
}

func (s *Service) evidenceID(ctx context.Context, tx pgx.Tx, ref string) (string, error) {
	el, err := s.eng.Get(ctx, tx, sysActor, ref)
	if err != nil {
		return "", err
	}
	if el.TypeKey != EvidenceType {
		return "", engine.ErrNotFound
	}
	return el.ID, nil
}

// ValidateInput è la validazione di un'evidenza.
type ValidateInput struct {
	ValidUntil string `json:"valid_until"`
	Note       string `json:"note"`
}

// Validate valida un'evidenza in bozza e la firma (grc.admin, mai chi l'ha caricata).
func (s *Service) Validate(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ValidateInput) (*EvidenceView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	id, err := s.evidenceID(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "select 1 from ext_evidence where element_id = $1 for update", id); err != nil {
		return nil, err
	}
	sig, r, err := loadSig(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if r.status != "draft" {
		return nil, engine.InvalidCode("validation.grc.evidence_only_draft", "status", "si valida solo un'evidenza in bozza")
	}
	if r.createdBy != "" && r.createdBy == actor.MemberID {
		return nil, engine.Required("validated_by", "chi ha caricato l'evidenza non la può validare: serve un'altra persona")
	}
	if r.kind == "attachment" && len(r.attachments) == 0 {
		return nil, engine.InvalidCode("validation.grc.evidence_no_attachments", "attachments", "allega almeno un file prima di validare")
	}
	until, err := parseDay("valid_until", in.ValidUntil)
	if err != nil {
		return nil, err
	}
	at := s.Now().UTC().Truncate(time.Microsecond)
	if until == nil {
		until = r.validUntil
	}
	if until == nil {
		st, err := loadSettings(ctx, tx)
		if err != nil {
			return nil, err
		}
		days := st.DefaultValidityDays
		rows, err := tx.Query(ctx, `select x.frequency from grc_evidence_controls g join ext_control x on x.element_id = g.control_element_id
			where g.evidence_element_id = $1`, id)
		if err != nil {
			return nil, err
		}
		freqs, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		for i, f := range freqs {
			if d := validityDays(f, st); i == 0 || d < days {
				days = d
			}
		}
		t := at.AddDate(0, 0, days)
		until = &t
	}
	u := until.UTC().Truncate(time.Microsecond)
	if !u.After(at) {
		return nil, engine.InvalidCode("validation.grc.valid_until_past", "valid_until", "la scadenza è già passata")
	}
	sig.validUntil, sig.signer, sig.at = &u, actor.MemberID, at
	signature := sig.signature()
	if _, err := tx.Exec(ctx, `update ext_evidence set status = 'valid', valid_until = $2, validated_by = $3, validated_at = $4, signature = $5,
		status_note = $6 where element_id = $1`, id, u, actor.MemberID, at, signature, strings.TrimSpace(in.Note)); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "grc", "validata", map[string]any{"valid_until": u.Format(time.DateOnly), "signature": signature}, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.evidence.validate", EvidenceType, id, map[string]any{"signature": signature, "valid_until": u}); err != nil {
		return nil, err
	}
	return s.Evidence(ctx, tx, actor, id)
}

// SetStatus respinge un'evidenza in bozza (rejected) o revoca una valida (revoked).
func (s *Service) SetEvidenceStatus(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, status, note string) (*EvidenceView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	note = strings.TrimSpace(note)
	if note == "" || len(note) > 2000 {
		return nil, engine.TooLong("note", 2000, "scrivi il motivo (massimo 2000 caratteri)")
	}
	id, err := s.evidenceID(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	from := map[string]string{"rejected": "draft", "revoked": "valid"}[status]
	if from == "" {
		return nil, engine.InvalidValue("status", "stato non ammesso (rejected, revoked)")
	}
	tag, err := tx.Exec(ctx, "update ext_evidence set status = $2, status_note = $3 where element_id = $1 and status = $4", id, status, note, from)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, engine.InvalidValue("status", "passaggio non ammesso dallo stato attuale")
	}
	if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "grc", status, note, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.evidence."+status, EvidenceType, id, nil); err != nil {
		return nil, err
	}
	return s.Evidence(ctx, tx, actor, id)
}

// AutoEvidence è un'evidenza automatica: un rilascio approvato, una scansione, una revisione
// degli accessi. È il punto di aggancio del modulo DevSecOps (collettore "devsecops.release")
// e di ogni altro collettore: l'evidenza si collega ai controlli indicati e a quelli iscritti
// al collettore (ext_control.collectors). Con lo stesso collettore e riferimento esterno
// l'invio è idempotente.
type AutoEvidence struct {
	Collector   string     `json:"collector"`
	ExternalRef string     `json:"external_ref"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	URL         string     `json:"url"`
	Application string     `json:"application"` // codice (API-CLI) o chiave dell'applicazione; vuoto = tutto il tenant
	Controls    []string   `json:"controls"`
	OccurredAt  *time.Time `json:"occurred_at"`
	ValidUntil  string     `json:"valid_until"`
}

// AutoResult è l'esito di un invio automatico.
type AutoResult struct {
	Key      string   `json:"key"`
	Created  bool     `json:"created"`
	Controls []string `json:"controls"`
}

// RecordAutomatic registra un'evidenza automatica (grc.collect o grc.admin).
func (s *Service) RecordAutomatic(ctx context.Context, tx pgx.Tx, actor engine.Actor, in AutoEvidence) (*AutoResult, error) {
	if !actor.Has(PermCollect) && !actor.Has(PermAdmin) {
		return nil, engine.ErrForbidden
	}
	if !collectorRe.MatchString(in.Collector) || in.Collector == CollectorVerification {
		return nil, engine.InvalidValue("collector", "collettore non valido (es. devsecops.release)")
	}
	in.ExternalRef = strings.TrimSpace(in.ExternalRef)
	if in.ExternalRef == "" || len(in.ExternalRef) > 200 {
		return nil, engine.TooLong("external_ref", 200, "riferimento esterno obbligatorio (massimo 200 caratteri), per l'idempotenza")
	}
	if ok, err := configured(ctx, tx); err != nil || !ok {
		return nil, errors.Join(err, engine.InvalidCode("validation.grc.module_not_configured", "collector", "modulo della conformità non configurato nel tenant"))
	}
	var existing string
	err := tx.QueryRow(ctx, `select s.key || '-' || e.number from ext_evidence x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id where x.collector = $1 and x.external_ref = $2`, in.Collector, in.ExternalRef).Scan(&existing)
	if err == nil {
		return &AutoResult{Key: existing, Created: false, Controls: []string{}}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	in.URL = strings.TrimSpace(in.URL)
	if in.URL != "" {
		if err := checkURL(in.URL); err != nil {
			return nil, err
		}
	}
	if len(in.Summary) > 20000 {
		return nil, engine.InvalidCode("validation.grc.text_too_long", "summary", "sintesi troppo lunga")
	}
	sc := tenantScope
	if strings.TrimSpace(in.Application) != "" {
		if sc, err = s.resolveScope(ctx, tx, "application", in.Application); err != nil {
			return nil, err
		}
	}
	ids := []string{}
	for _, k := range in.Controls {
		id, err := s.controlRef(ctx, tx, k)
		if err != nil {
			return nil, engine.NotFound("controls", "control", "controllo %q inesistente", k)
		}
		ids = append(ids, id)
	}
	rows, err := tx.Query(ctx, "select element_id::text from ext_control where $1 = any(collectors)", in.Collector)
	if err != nil {
		return nil, err
	}
	subscribed, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	for _, id := range subscribed {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	at := s.Now().UTC().Truncate(time.Microsecond)
	from := at
	if in.OccurredAt != nil {
		from = in.OccurredAt.UTC().Truncate(time.Microsecond)
	}
	until, err := parseDay("valid_until", in.ValidUntil)
	if err != nil {
		return nil, err
	}
	if until == nil {
		st, err := loadSettings(ctx, tx)
		if err != nil {
			return nil, err
		}
		t := from.AddDate(0, 0, st.DefaultValidityDays)
		until = &t
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = in.Collector + " " + in.ExternalRef
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: EvidenceType, Title: title, BodyText: strings.TrimSpace(in.Summary)})
	if err != nil {
		return nil, err
	}
	kind := "automatic"
	if strings.HasPrefix(in.Collector, "devsecops.") {
		kind = "release"
	}
	u := until.UTC().Truncate(time.Microsecond)
	elT, ou := scopeTargets(sc)
	target := sc.ID
	sig := sigInput{id: el.ID, kind: kind, title: el.Title, body: strings.TrimSpace(in.Summary), url: in.URL, scopeKind: sc.Kind, target: target,
		attachments: []string{}, validFrom: from, validUntil: &u, signer: "collector:" + in.Collector + ":" + actor.MemberID, at: at}
	if _, err := tx.Exec(ctx, `insert into ext_evidence (tenant_id, element_id, source, collector, external_ref, kind, url, scope_kind, target_element_id,
		target_org_unit_id, valid_from, valid_until, status, validated_by, validated_at, signature, created_by)
		values (app_tenant(), $1, 'automatic', $2, $3, $4, $5, $6, $7, $8, $9, $10, 'valid', $11, $12, $13, $11)`,
		el.ID, in.Collector, in.ExternalRef, kind, nullable(in.URL), sc.Kind, elT, ou, from, u, nullable(actor.MemberID), at, sig.signature()); err != nil {
		return nil, err
	}
	if err := linkControls(ctx, tx, el.ID, ids); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.evidence.automatic", EvidenceType, el.ID, map[string]any{"collector": in.Collector,
		"external_ref": in.ExternalRef, "controls": ids}); err != nil {
		return nil, err
	}
	keys := []string{}
	if len(ids) > 0 {
		rows, err := tx.Query(ctx, `select s.key || '-' || e.number from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			where e.id = any($1) order by e.number`, ids)
		if err != nil {
			return nil, err
		}
		if keys, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return nil, err
		}
	}
	return &AutoResult{Key: el.Key, Created: true, Controls: keys}, nil
}

// ---------------------------------------------------------------------
// Viste

// EvidenceView è il dettaglio di un'evidenza.
type EvidenceView struct {
	EvidenceRef
	Note        string        `json:"note"`
	URL         string        `json:"url"`
	Page        *ElementLink  `json:"page"`
	Source      string        `json:"source"`
	Collector   string        `json:"collector"`
	ExternalRef string        `json:"external_ref"`
	ValidFrom   time.Time     `json:"valid_from"`
	ValidatedAt *time.Time    `json:"validated_at"`
	Signature   string        `json:"signature"`
	SignatureOK bool          `json:"signature_ok"`
	StatusNote  string        `json:"status_note"`
	CreatedBy   string        `json:"created_by"`
	Linked      []ElementLink `json:"linked_controls"`
	Attachments int           `json:"attachments"`
	Can         EvidenceCan   `json:"can"`
}

// ElementLink è un elemento citato.
type ElementLink struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
}

// EvidenceCan sono le azioni permesse sull'evidenza.
type EvidenceCan struct {
	Validate bool `json:"validate"`
	Reject   bool `json:"reject"`
	Revoke   bool `json:"revoke"`
}

// Evidence restituisce un'evidenza: la vede chi ha grc.read o è responsabile di un suo controllo.
func (s *Service) Evidence(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*EvidenceView, error) {
	id, err := s.evidenceID(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	sig, r, err := loadSig(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select e.id, s.key || '-' || e.number, e.title, x.owner_id = $2
		from grc_evidence_controls g join elements e on e.tenant_id = g.tenant_id and e.id = g.control_element_id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join ext_control x on x.tenant_id = g.tenant_id and x.element_id = g.control_element_id
		where g.evidence_element_id = $1 order by e.number`, id, nullable(actor.MemberID))
	if err != nil {
		return nil, err
	}
	linked := []ElementLink{}
	owner := false
	var l ElementLink
	var own *bool
	if _, err := pgx.ForEachRow(rows, []any{&l.ID, &l.Key, &l.Title, &own}, func() error {
		linked = append(linked, l)
		if own != nil && *own {
			owner = true
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if !CanRead(actor) && !owner {
		return nil, engine.ErrForbidden
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	m := &model{now: s.Now(), settings: st, scopes: map[string]Scope{tenantScope.id(): tenantScope}}
	m.notice = m.now.AddDate(0, 0, st.ExpiryNoticeDays)
	sc := tenantScope
	if r.target != "" {
		if sc, err = s.scopeByID(ctx, tx, r.scopeKind, r.target); err != nil {
			return nil, err
		}
	}
	m.scopes[sc.id()] = sc
	var validatedBy, createdBy string
	if err := tx.QueryRow(ctx, `select coalesce((select display_name from members where id::text = $1), ''),
		coalesce((select display_name from members where id::text = $2), '')`, r.validatedBy, r.createdBy).Scan(&validatedBy, &createdBy); err != nil {
		return nil, err
	}
	ev := &evModel{ID: r.id, Key: r.key, Title: r.title, Kind: r.kind, Collector: r.collector, Status: r.status, Scope: sc.id(),
		ValidFrom: r.validFrom, ValidUntil: r.validUntil, ValidatedBy: validatedBy, ValidatedAt: r.validatedAt, Signature: r.signature}
	v := &EvidenceView{EvidenceRef: m.evRef(ev), Note: r.body, URL: r.url, Source: r.source, Collector: r.collector, ValidFrom: r.validFrom,
		ValidatedAt: r.validatedAt, Signature: r.signature, CreatedBy: createdBy, Linked: linked, Attachments: len(r.attachments),
		Can: EvidenceCan{Validate: actor.Has(PermAdmin) && r.status == "draft" && r.createdBy != actor.MemberID,
			Reject: actor.Has(PermAdmin) && r.status == "draft", Revoke: actor.Has(PermAdmin) && r.status == "valid"}}
	var ext *string
	if err := tx.QueryRow(ctx, "select external_ref, status_note from ext_evidence where element_id = $1", id).Scan(&ext, &v.StatusNote); err != nil {
		return nil, err
	}
	v.ExternalRef = deref(ext)
	if r.page != "" {
		pg := &ElementLink{}
		if err := tx.QueryRow(ctx, `select e.id, s.key || '-' || e.number, e.title from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			where e.id = $1`, r.page).Scan(&pg.ID, &pg.Key, &pg.Title); err == nil {
			v.Page = pg
		}
	}
	if r.validatedAt != nil && r.signature != "" {
		sig.validUntil, sig.at, sig.signer = r.validUntil, *r.validatedAt, r.signer()
		v.SignatureOK = sig.signature() == r.signature
	}
	return v, nil
}

// scopeByID ricostruisce un perimetro dal suo id.
func (s *Service) scopeByID(ctx context.Context, tx pgx.Tx, kind, id string) (Scope, error) {
	sc := Scope{Kind: kind, ID: id}
	var err error
	if kind == "legal_entity" {
		err = tx.QueryRow(ctx, "select coalesce(code, ''), name from org_units where id::text = $1", id).Scan(&sc.Key, &sc.Name)
	} else {
		err = tx.QueryRow(ctx, `select s.key || '-' || e.number, e.title from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			where e.id::text = $1`, id).Scan(&sc.Key, &sc.Name)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return sc, nil
	}
	return sc, err
}

// EvidenceList elenca le evidenze (anche le verifiche dei requisiti di progetto), con filtro
// sullo stato: valid, expiring, expired, draft, rejected, revoked (vuoto = tutte).
func (s *Service) EvidenceList(ctx context.Context, tx pgx.Tx, actor engine.Actor, status string) ([]EvidenceRef, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return nil, err
	}
	byID := map[string]*EvidenceRef{}
	order := []string{}
	for _, c := range m.ctrlOrder {
		for _, ev := range c.evidence {
			er := byID[ev.ID]
			if er == nil {
				ref := m.evRef(ev)
				er = &ref
				byID[ev.ID] = er
				order = append(order, ev.ID)
			}
			if !containsStr(er.Controls, c.Key) {
				er.Controls = append(er.Controls, c.Key)
			}
		}
	}
	out := []EvidenceRef{}
	for _, id := range order {
		if er := byID[id]; status == "" || er.Status == status {
			out = append(out, *er)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].ValidUntil, out[j].ValidUntil
		if a == nil || b == nil {
			return b == nil && a != nil
		}
		return a.Before(*b)
	})
	return out, nil
}
