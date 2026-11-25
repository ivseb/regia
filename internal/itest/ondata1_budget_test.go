package itest

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/finance"
	"regia/internal/modules/portfolio"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Il budget della graduatoria viene dal plafond dell'esercizio in corso: il collegamento nasce da solo se
// nessuno ha già scelto, e la graduatoria dice da dove viene il budget (o propone l'esercizio).
func TestGraduatoriaBudgetDalPlafond(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	mk := func(prefix string) (*user, *user) {
		slug := strings.ReplaceAll(prefix+"-"+time.Now().Format("150405.000000"), ".", "")
		if _, err := tenant.Create(ctx, d, tenant.CreateInput{
			Slug: slug, Name: "Budget " + prefix, AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
			Provision: []tenant.Provisioner{demand.Provision, architecture.Provision, portfolio.Provision, finance.Provision},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: "fin@" + slug + ".test", Name: "Finanza",
			Password: testPassword, Roles: []string{"editor", "finance"}}); err != nil {
			t.Fatal(err)
		}
		eng := engine.New()
		ap := approval.Register(eng)
		dm := demand.Register(eng, ap)
		arc := architecture.Register(eng)
		apiSrv := api.New(api.Options{
			DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc,
			Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
			Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
		})
		srv := httptest.NewServer(apiSrv.Handler())
		t.Cleanup(func() { srv.Close(); apiSrv.Close() })
		return login(t, srv.URL, "admin@"+slug+".test", testPassword), login(t, srv.URL, "fin@"+slug+".test", testPassword)
	}
	year := time.Now().Year()
	fy := map[string]any{"name": fmt.Sprintf("Esercizio %d", year), "start_date": fmt.Sprintf("%d-01-01", year), "end_date": fmt.Sprintf("%d-12-31", year),
		"envelope_cents": 120_000_000}

	// Nessuna scelta precedente: il primo esercizio in corso con un plafond alimenta la graduatoria.
	admin, fin := mk("bud1")
	src := admin.do("GET", "/api/v1/portfolio/ranking", nil, http.StatusOK)["budget_source"].(map[string]any)
	if src["kind"] != "none" || src["suggested"] != nil {
		t.Fatalf("prima di un esercizio: %v", src)
	}
	fin.do("POST", "/api/v1/finance/fiscal-years", fy, http.StatusCreated)
	rk := admin.do("GET", "/api/v1/portfolio/ranking", nil, http.StatusOK)
	src = rk["budget_source"].(map[string]any)
	if src["kind"] != "fiscal_year" || src["fiscal_year"] != fy["name"] || num(rk["budget_cents"]) != 120_000_000 {
		t.Fatalf("collegamento automatico: %v budget=%v", src, rk["budget_cents"])
	}

	// Con un budget impostato a mano il collegamento non scavalca la scelta: si propone soltanto.
	admin, fin = mk("bud2")
	admin.do("PUT", "/api/v1/portfolio/settings", map[string]any{"budget_cents": 60_000_000}, http.StatusOK)
	fin.do("POST", "/api/v1/finance/fiscal-years", fy, http.StatusCreated)
	rk = admin.do("GET", "/api/v1/portfolio/ranking", nil, http.StatusOK)
	src = rk["budget_source"].(map[string]any)
	sug, _ := src["suggested"].(map[string]any)
	if src["kind"] != "manual" || sug == nil || num(sug["envelope_cents"]) != 120_000_000 || num(rk["budget_cents"]) != 60_000_000 {
		t.Fatalf("budget a mano: %v budget=%v", src, rk["budget_cents"])
	}
	// Un clic sul suggerimento (le impostazioni della Finanza) collega l'esercizio.
	fin.do("PUT", "/api/v1/finance/settings", map[string]any{"portfolio_fiscal_year_id": sug["id"]}, http.StatusOK)
	rk = admin.do("GET", "/api/v1/portfolio/ranking", nil, http.StatusOK)
	if k := rk["budget_source"].(map[string]any)["kind"]; k != "fiscal_year" || num(rk["budget_cents"]) != 120_000_000 {
		t.Fatalf("dopo il collegamento: %v budget=%v", rk["budget_source"], rk["budget_cents"])
	}
}
