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

	"github.com/jackc/pgx/v5"

	"regia/internal/ai"
	"regia/internal/api"
	"regia/internal/devtools/fakeai"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/demand"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Ragionamento configurabile per livello e per funzione, e riassunto delle conversazioni
// lunghe, con il fornitore finto a scenari (nessuna chiave vera).
func TestAIRagionamentoERiassunto(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	slug := "air-" + stamp
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "AI " + slug, AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: []tenant.Provisioner{demand.Provision}}); err != nil {
		t.Fatal(err)
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	apiSrv := api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	})
	srv := httptest.NewServer(apiSrv.Handler())
	defer srv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	fake := admin.do("POST", "/api/v1/ai/providers", map[string]any{"kind": "fake", "name": "Finto"}, http.StatusCreated)
	fakeID := fake["id"].(string)
	conv, sum := "conv-"+stamp, "sum-"+stamp
	deep := map[string]any{"provider_id": fakeID, "model": "scenario:" + conv, "reasoning": "medium"}
	fast := map[string]any{"provider_id": fakeID, "model": "scenario:" + sum}

	// --- Valori non ammessi: rifiutati, sul livello e sulla funzione.
	admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true, "deep": map[string]any{"provider_id": fakeID, "model": "x", "reasoning": "max"}}, http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true, "deep": deep,
		"routes": map[string]any{"bozze": map[string]any{"reasoning": "alto"}}}, http.StatusUnprocessableEntity)

	// --- Salvati e riletti: il livello, e una funzione con il solo ragionamento (senza cambiare livello).
	saved := obj(admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true, "deep": deep, "fast": fast,
		"routes": map[string]any{"bozze": map[string]any{"reasoning": "high"}, "mancanze": map[string]any{}}}, http.StatusOK)["settings"])
	if obj(saved["deep"])["reasoning"] != "medium" || obj(saved["fast"])["reasoning"] != nil {
		t.Fatalf("ragionamento dei livelli: %v", saved)
	}
	routes := obj(saved["routes"])
	if len(routes) != 1 || obj(routes["bozze"])["reasoning"] != "high" || obj(routes["bozze"])["tier"] != nil {
		t.Fatalf("ragionamento delle funzioni: %v", routes)
	}

	// --- Conversazione lunga: oltre la finestra i messaggi vecchi vanno nel riassunto.
	const asks = 7 // 7 domande: alla settima la storia ha 12 messaggi, 2 escono dalla finestra di 10
	steps := make([]fakeai.Step, asks)
	for i := range steps {
		steps[i] = fakeai.Step{Text: fmt.Sprintf("risposta %d", i+1)}
	}
	fakeai.Program(conv, steps...)
	fakeai.Program(sum, fakeai.Step{Text: "- si parlava del magazzino zeta"})
	convID := ""
	for i := 1; i <= asks; i++ {
		body := map[string]any{"question": fmt.Sprintf("domanda %d", i)}
		if convID != "" {
			body["conversation_id"] = convID
		}
		res := admin.do("POST", "/api/v1/ai/ask", body, http.StatusOK)
		convID = obj(res["conversation"])["id"].(string)
	}
	reqs := fakeai.Requests(conv)
	if len(reqs) != asks {
		t.Fatalf("richieste all'assistente: %d", len(reqs))
	}
	// Il ragionamento del livello arriva all'adattatore.
	if reqs[0].Effort != ai.EffortMedium {
		t.Fatalf("ragionamento dell'assistente: %q", reqs[0].Effort)
	}
	// Prima della settima nessun riassunto; alla settima il riassunto è in testa, come dato,
	// e la prima domanda non c'è più per intero.
	if strings.Contains(reqs[5].Messages[0].Text, "riassunto della conversazione") {
		t.Fatal("riassunto prima del tempo")
	}
	last := reqs[asks-1]
	first := last.Messages[0].Text
	if !strings.Contains(first, "riassunto della conversazione precedente") || !strings.Contains(first, "magazzino zeta") {
		t.Fatalf("riassunto non in testa: %q", first)
	}
	for _, m := range last.Messages {
		if strings.Contains(m.Text, "domanda 1\n") || m.Text == "domanda 1" {
			t.Fatal("la prima domanda è ancora nella finestra")
		}
	}
	if len(last.Messages) != 11 { // 10 della finestra (dal primo della persona) + la domanda nuova
		t.Fatalf("messaggi della settima: %d", len(last.Messages))
	}
	sreqs := fakeai.Requests(sum)
	if len(sreqs) != 1 || !strings.Contains(sreqs[0].Messages[0].Text, "domanda 1") || sreqs[0].Effort != ai.EffortLow {
		t.Fatalf("richiesta del riassunto: %+v", sreqs)
	}
	var summary string
	var upto int
	tid := ""
	if err := d.Owner.QueryRow(ctx, `select id::text from tenants where slug = $1`, slug).Scan(&tid); err != nil {
		t.Fatal(err)
	}
	if err := d.InTenant(ctx, tid, db.TenantOpts{}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select summary, summary_upto from ai_conversations where id = $1`, convID).Scan(&summary, &upto)
	}); err != nil {
		t.Fatal(err)
	}
	if upto != 2 || !strings.Contains(summary, "magazzino zeta") {
		t.Fatalf("riassunto salvato: %d %q", upto, summary)
	}
	var n int
	if err := d.InTenant(ctx, tid, db.TenantOpts{}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select count(*) from ai_calls where feature = 'riassunti' and conversation_id = $1 and outcome = 'ok'`, convID).Scan(&n)
	}); err != nil || n != 1 {
		t.Fatalf("riassunto nel registro: %d %v", n, err)
	}
}
