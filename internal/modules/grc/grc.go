// Package grc è la parte "conformità" del GRC (piano, passo 20; replica/grc.md):
//   - cataloghi normativi del tenant come dati, versionati, importabili ed esportabili in
//     OSCAL, CSV e JSON, con i pacchetti di partenza (DORA, NIS2, ISO/IEC 27001 Annex A,
//     GDPR, una struttura vuota per AgID/ACN) e le corrispondenze tra normative;
//   - controlli interni (elemento "control") che coprono requisiti di più normative: una
//     evidenza vale una volta sola per tutte;
//   - applicabilità di requisiti e controlli a perimetri (tenant, entità legale,
//     applicazione, processo), decisa a mano o da regole sui fatti degli elementi;
//   - evidenze manuali o automatiche, con validità, scadenza e firma di chi le valida; le
//     verifiche dei requisiti di progetto collegati a un requisito normativo valgono come
//     evidenze dei controlli che lo coprono;
//   - copertura per normativa e per applicazione, fino all'evidenza, e pacchetto per l'audit.
//
// Rischi, fornitori ICT e incidenti con le notifiche sono un modulo separato.
package grc

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

//go:embed starter/*.json starter/catalogs/*.json
var starters embed.FS

const (
	SpaceKey     = "GRC"
	ControlType  = "control"
	EvidenceType = "evidence"
	AppType      = "application"

	// Permessi.
	PermAdmin   = "grc.admin"   // cataloghi, controlli, applicabilità, validazione delle evidenze
	PermRead    = "grc.read"    // cruscotto, cataloghi, controlli ed evidenze in lettura (auditor)
	PermCollect = "grc.collect" // evidenze automatiche dalle pipeline e dai collettori

	// Ruoli sul controllo.
	RoleOwner    = "control_owner" // responsabile del controllo
	RoleReviewer = "grc_reviewer"  // la compliance che lo segue

	// GroupCompliance è il gruppo della compliance (esiste già se ci sono le valutazioni).
	GroupCompliance = "Compliance"

	// Formati di scambio.
	CatalogFormat = "regia.grc.catalog/v1"
	PackageFormat = "regia.grc/v1"

	// CollectorVerification: evidenza che nasce dalla verifica di un requisito di progetto.
	CollectorVerification = "requirement_verification"
)

// Service è il modulo. Now si sostituisce nei test per le scadenze.
type Service struct {
	eng *engine.Service
	Now func() time.Time
}

// Register collega il modulo al motore: fatti del controllo e dell'applicazione, pacchetto
// di partenza del processo del controllo.
func Register(eng *engine.Service) *Service {
	s := &Service{eng: eng, Now: time.Now}
	eng.Facts(ControlType, s.controlFacts)
	eng.Facts(AppType, s.appFacts)
	eng.DescribeFacts(ControlType,
		engine.FactInfo{Key: "responsabile", Name: "Responsabile del controllo", Kind: "text"},
		engine.FactInfo{Key: "frequenza", Name: "Frequenza di verifica", Kind: "text", Values: []string{"continua", "mensile", "trimestrale", "semestrale", "annuale", "biennale"}},
		engine.FactInfo{Key: "efficacia", Name: "Efficacia", Kind: "text", Values: []string{"non valutata", "efficace", "parziale", "inefficace"}},
		engine.FactInfo{Key: "requisiti", Name: "Requisiti normativi coperti", Kind: "number", Example: "4"},
		engine.FactInfo{Key: "normative", Name: "Normative coperte", Kind: "text", Example: "dora, iso27001"},
		engine.FactInfo{Key: "evidenze_valide", Name: "Evidenze valide", Kind: "number", Example: "2"},
		engine.FactInfo{Key: "evidenze_in_scadenza", Name: "Evidenze in scadenza", Kind: "number", Example: "0"},
	)
	eng.DescribeFacts(AppType,
		engine.FactInfo{Key: "normative", Name: "Normative a cui è soggetta (conformità)", Kind: "text", Example: "dora, gdpr"},
	)
	raw, err := starters.ReadFile("starter/control_standard.json")
	if err != nil {
		panic(err)
	}
	eng.Starter(raw)
	return s
}

// CanRead dice se l'attore vede il cruscotto e i cataloghi.
func CanRead(a engine.Actor) bool { return a.Has(PermRead) || a.Has(PermAdmin) }

func requireRead(a engine.Actor) error {
	if !CanRead(a) {
		return engine.ErrForbidden
	}
	return nil
}

func requireAdmin(a engine.Actor) error {
	if !a.Has(PermAdmin) {
		return engine.ErrForbidden
	}
	return nil
}

func audited(ctx context.Context, tx pgx.Tx, actor engine.Actor, action, entity, id string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: entity, EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// configured dice se il tenant ha il modulo (tipo "control"): il modulo è registrato sul
// motore anche nei tenant non ancora configurati, dove non fa nulla.
func configured(ctx context.Context, tx pgx.Tx) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, "select exists (select 1 from element_types where key = $1)", ControlType).Scan(&ok)
	return ok, err
}

// Settings sono le impostazioni del tenant.
type Settings struct {
	ExpiryNoticeDays    int        `json:"expiry_notice_days"`
	DefaultValidityDays int        `json:"default_validity_days"`
	ProcessTypes        []string   `json:"process_types"`
	RecomputedAt        *time.Time `json:"recomputed_at,omitempty"`
}

func loadSettings(ctx context.Context, tx pgx.Tx) (*Settings, error) {
	st := &Settings{ExpiryNoticeDays: 30, DefaultValidityDays: 365, ProcessTypes: []string{"business_process"}}
	err := tx.QueryRow(ctx, `select expiry_notice_days, default_validity_days, process_types, recomputed_at from grc_settings`).
		Scan(&st.ExpiryNoticeDays, &st.DefaultValidityDays, &st.ProcessTypes, &st.RecomputedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	return st, err
}

// UpdateSettings salva le impostazioni (grc.admin).
func (s *Service) UpdateSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in Settings) (*Settings, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if in.ExpiryNoticeDays < 0 || in.ExpiryNoticeDays > 365 {
		return nil, engine.OutOfRange("expiry_notice_days", 0, 365, "preavviso tra 0 e 365 giorni")
	}
	if in.DefaultValidityDays < 1 || in.DefaultValidityDays > 1825 {
		return nil, engine.OutOfRange("default_validity_days", 1, 1825, "validità tra 1 e 1825 giorni")
	}
	if in.ProcessTypes == nil {
		in.ProcessTypes = []string{}
	}
	for _, t := range in.ProcessTypes {
		if !keyRe.MatchString(t) {
			return nil, engine.InvalidValue("process_types", "tipo di elemento %q non valido", t)
		}
	}
	if _, err := tx.Exec(ctx, `insert into grc_settings (tenant_id, expiry_notice_days, default_validity_days, process_types, updated_by)
		values (app_tenant(), $1, $2, $3, $4)
		on conflict (tenant_id) do update set expiry_notice_days = excluded.expiry_notice_days,
		  default_validity_days = excluded.default_validity_days, process_types = excluded.process_types,
		  updated_by = excluded.updated_by, updated_at = now()`,
		in.ExpiryNoticeDays, in.DefaultValidityDays, in.ProcessTypes, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.settings.update", "grc_settings", "", map[string]any{
		"expiry_notice_days": in.ExpiryNoticeDays, "default_validity_days": in.DefaultValidityDays}); err != nil {
		return nil, err
	}
	return loadSettings(ctx, tx)
}

// Frequenze del controllo: nome e durata di validità di un'evidenza.
var frequencies = map[string]struct {
	name string
	days int
}{
	"continuous": {"continua", 31},
	"monthly":    {"mensile", 31},
	"quarterly":  {"trimestrale", 92},
	"semiannual": {"semestrale", 183},
	"annual":     {"annuale", 366},
	"biennial":   {"biennale", 731},
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// validityDays è la durata di un'evidenza per un controllo con quella frequenza.
func validityDays(freq string, st *Settings) int {
	if f, ok := frequencies[freq]; ok {
		return f.days
	}
	return st.DefaultValidityDays
}

var effectivenessNames = map[string]string{"not_assessed": "non valutata", "effective": "efficace", "partial": "parziale", "ineffective": "inefficace"}
var controlKinds = map[string]string{"preventive": "preventivo", "detective": "di rilevazione", "corrective": "correttivo"}
var automations = map[string]string{"manual": "manuale", "semi": "semiautomatico", "automatic": "automatico"}
