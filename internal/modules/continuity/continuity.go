// Package continuity è la continuità operativa (docs/sviluppo/continuita.md): BIA per capability
// con versioni approvate, RTO/RPO richiesti contro dichiarati lungo il grafo dell'architettura,
// piani di continuità (documenti controllati della Knowledge) e test di resilienza con esito che
// diventa evidenza GRC. Le incoerenze si calcolano a ogni lettura e vanno al «dal problema
// all'azione» come problemi di tipo «continuity».
package continuity

import (
	"context"
	"embed"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/grc"
	"regia/internal/platform/rbac"
)

const (
	SpaceKey       = "ARC"
	BIAType        = "bia"
	ResTestType    = "resilience_test"
	AppType        = "application"
	CapabilityType = "capability"
	ProviderType   = "ict_provider"
	PageType       = "page"

	// RelPlanFor: pagina della Knowledge → capability o applicazione di cui è il piano di continuità.
	RelPlanFor  = "continuity_plan_for"
	RelSupports = "supports"
	RelProvided = "provided_by"

	// Collector è il collettore delle evidenze automatiche dei test.
	Collector = "continuity.test"

	// Fasi dei processi di partenza (le guardie e gli agganci le cercano per nome e categoria).
	PhaseBIAApproved = "Approvata"
	PhaseTestDone    = "Eseguito"

	PermWrite   = rbac.ContinuityWrite
	PermApprove = rbac.ContinuityApprove
	PermAdmin   = rbac.ContinuityAdmin
)

// Esiti di un test.
const (
	OutcomePassed   = "passed"
	OutcomeFindings = "passed_with_findings"
	OutcomeFailed   = "failed"
)

var outcomes = []string{OutcomePassed, OutcomeFindings, OutcomeFailed}

//go:embed starter/*.json
var starters embed.FS

// Service è il modulo.
type Service struct {
	eng *engine.Service
	grc *grc.Service
	Now func() time.Time
}

// Register collega il modulo al motore: fatti, gate, agganci alle transizioni, pacchetti di
// partenza, tipo di problema.
func Register(eng *engine.Service) *Service {
	s := &Service{eng: eng, Now: time.Now}
	eng.Facts(AppType, s.appFacts)
	eng.Facts(CapabilityType, s.capabilityFacts)
	eng.Facts(BIAType, s.biaFacts)
	eng.Facts(ResTestType, s.testFacts)
	describeFacts(eng)
	eng.Guard("bia_completa", s.guardBIA)
	eng.Guard("esito_test", s.guardTest)
	eng.OnTransition(s.afterTransition)
	for _, f := range starterFiles {
		raw, err := starters.ReadFile("starter/" + f)
		if err != nil {
			panic(err)
		}
		eng.Starter(raw)
	}
	registerProblems(s)
	return s
}

// UseGRC collega le evidenze automatiche dei test.
func (s *Service) UseGRC(g *grc.Service) { s.grc = g }

// Can sono le azioni permesse all'attore.
type Can struct {
	Write   bool `json:"write"`
	Approve bool `json:"approve"`
	Admin   bool `json:"admin"`
}

func can(a engine.Actor) Can {
	return Can{Write: a.Has(PermWrite), Approve: a.Has(PermApprove), Admin: a.Has(PermAdmin) || a.Has(rbac.TenantAdmin)}
}

func canRead(a engine.Actor) bool { return a.Has(rbac.ElementRead) }

// isReady dice se il modulo è stato preparato nel tenant (tipi e configurazione).
func isReady(ctx context.Context, tx pgx.Tx) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `select exists (select 1 from element_types where key = $1) and exists (select 1 from continuity_settings)`, BIAType).Scan(&ok)
	return ok, err
}

func requireReady(ctx context.Context, tx pgx.Tx) error {
	ok, err := isReady(ctx, tx)
	if err != nil {
		return err
	}
	if !ok {
		return engine.InvalidCode("validation.continuity.not_ready", "continuity", "la continuità operativa non è ancora preparata in questo tenant")
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func today(now time.Time) time.Time {
	y, m, d := now.In(rome()).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

var romeLoc *time.Location

func rome() *time.Location {
	if romeLoc == nil {
		if l, err := time.LoadLocation("Europe/Rome"); err == nil {
			romeLoc = l
		} else {
			romeLoc = time.UTC
		}
	}
	return romeLoc
}
