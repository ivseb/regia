package itest

import (
	"context"
	"strings"
	"testing"
	"time"

	"regia/internal/modules/architecture"
	"regia/internal/modules/continuity"
	"regia/internal/modules/demand"
	"regia/internal/modules/finance"
	"regia/internal/modules/grc"
	"regia/internal/modules/knowledge"
	"regia/internal/modules/portfolio"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/modules/risk"
	"regia/internal/modules/work"
	"regia/internal/platform/tenant"
)

// Il provisioning con tutti i moduli, nell'ordine di cmd/regia, deve riuscire sia alla
// creazione del tenant sia quando si ripete su un tenant esistente. Ogni modulo valida i
// processi con un motore che conosce solo le sue azioni: quelle degli altri moduli già
// pubblicate vanno accettate (regressione del 2026-11-24: create_epic e requisiti).
func TestProvisionAllModules(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	all := []tenant.Provisioner{demand.Provision, architecture.Provision, portfolio.Provision, finance.Provision,
		reviews.Provision, work.Provision, requirements.Provision, risk.Provision, grc.Provision}
	// Knowledge (piano, passo 19): tipo pagina, documento controllato, template.
	all = append(all, knowledge.Provision)
	// Continuità operativa: dopo architettura e Knowledge (tipi della relazione dei piani).
	all = append(all, continuity.Provision)
	slug := strings.ReplaceAll("tutti-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Tutti i moduli",
		AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: all}); err != nil {
		t.Fatal(err)
	}
	if err := tenant.Provision(ctx, d, slug, all); err != nil {
		t.Fatalf("secondo provisioning: %v", err)
	}
	// Ordine diverso: prima i requisiti, poi il work management.
	slug2 := slug + "b"
	rev := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision, work.Provision, risk.Provision, grc.Provision}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug2, Name: "Ordine inverso",
		AdminEmail: "admin@" + slug2 + ".test", AdminPassword: testPassword, Provision: rev}); err != nil {
		t.Fatal(err)
	}
	if err := tenant.Provision(ctx, d, slug2, all); err != nil {
		t.Fatalf("provisioning dopo l'ordine inverso: %v", err)
	}
}
