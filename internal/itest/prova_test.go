package itest

import (
	"context"
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
	"regia/internal/modules/grc"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Ambiente di prova e promuovi (ondata 3, configurazione.md §5): la prova è un tenant collegato con una
// copia della configurazione; cambiare un processo e un campo lì non tocca il principale; il confronto
// dice cosa è nuovo e cosa è cambiato; si promuove solo ciò che si sceglie; tutto è nell'audit; la prova
// non vede i dati del principale (e viceversa); «ricrea» riallinea.
func TestAmbienteProvaPromuovi(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("prova-"+time.Now().Format("150405.000"), ".", "")
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision, grc.Provision, work.Provision}
	cr, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Prova", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: provision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: "ed@" + slug + ".test", Name: "ed", Password: testPassword, Roles: []string{"editor"}}); err != nil {
		t.Fatal(err)
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rev := reviews.Register(eng)
	rq := requirements.Register(eng, ap)
	gs := grc.Register(eng)
	wk := work.Register(eng)
	_ = wk
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Reviews: rev, Requirements: rq, GRC: gs,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	adm, ed := login(t, srv.URL, "admin@"+slug+".test", testPassword), login(t, srv.URL, "ed@"+slug+".test", testPassword)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	count := func(sql string, args ...any) int {
		var n int
		if err := d.Owner.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Solo l'amministratore.
	ed.do("POST", "/api/v1/prova", nil, http.StatusForbidden)
	ed.do("GET", "/api/v1/prova/differenze", nil, http.StatusForbidden)
	if st := adm.do("GET", "/api/v1/prova", nil, http.StatusOK); st["role"] != "none" || st["can_manage"] != true {
		t.Fatalf("stato iniziale: %v", st)
	}
	adm.do("GET", "/api/v1/prova/differenze", nil, http.StatusUnprocessableEntity) // nessuna prova collegata

	// Un elemento nel principale, con testo che non deve arrivare nella prova.
	adm.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Richiesta riservata del principale"}, http.StatusCreated)

	// Crea la prova (con campione) e una seconda volta no.
	adm.do("POST", "/api/v1/prova", map[string]any{"sample": true}, http.StatusCreated)
	adm.do("POST", "/api/v1/prova", nil, http.StatusUnprocessableEntity)
	st := adm.do("GET", "/api/v1/prova", nil, http.StatusOK)
	sbxID := obj(st["linked"])["tenant_id"].(string)
	if st["role"] != "principale" || obj(st["linked"])["slug"] != slug+"-prova" || st["with_sample"] != true {
		t.Fatalf("stato dopo la creazione: %v", st)
	}
	// Nessuna fuga: la prova ha solo l'amministratore, nessun testo del principale, e il principale non ha righe della prova.
	if n := count("select count(*) from members where tenant_id = $1", sbxID); n != 1 {
		t.Fatalf("la prova ha %d persone, deve averne una", n)
	}
	if n := count("select count(*) from elements where tenant_id = $1 and title like '%riservata%'", sbxID); n != 0 {
		t.Fatal("un titolo del principale è finito nella prova")
	}
	if n := count("select count(*) from elements e join element_types t on t.id = e.type_id and t.tenant_id = e.tenant_id where e.tenant_id = $1 and t.extension is not null", sbxID); n != 0 {
		t.Fatal("il campione contiene tipi con estensione di dominio")
	}
	if n := count("select count(*) from process_definitions where tenant_id = $1", sbxID); n == 0 {
		t.Fatal("i processi non sono stati copiati")
	}
	if n := count("select count(*) from process_versions where tenant_id = $1 and status <> 'published'", sbxID); n != 0 {
		t.Fatal("si copiano solo le versioni pubblicate")
	}

	// Il confronto parte vuoto: la prova è identica al principale.
	if diff := adm.do("GET", "/api/v1/prova/differenze", nil, http.StatusOK); len(list(diff["items"])) != 0 {
		t.Fatalf("differenze iniziali: %v", diff["items"])
	}

	// Entra nella prova e cambia un processo e un campo.
	sbxMember := obj(adm.do("POST", "/api/v1/me/tenant", map[string]any{"tenant_id": sbxID}, http.StatusOK)["active_tenant"])["member_id"]
	if st := adm.do("GET", "/api/v1/prova", nil, http.StatusOK); st["role"] != "prova" {
		t.Fatalf("nella prova: %v", st)
	}
	procs := adm.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK)
	def := obj(procs["current"])
	phases := list(def["phases"])
	obj(phases[0])["description"] = "Cambiata nella prova"
	adm.do("PUT", "/api/v1/processes/demand_standard/draft", map[string]any{"definition": def, "notes": "prova"}, http.StatusOK)
	adm.do("POST", "/api/v1/processes/demand_standard/publish", map[string]any{"notes": "prova"}, http.StatusOK)
	adm.do("POST", "/api/v1/fields", map[string]any{"key": "campo_prova", "name": "Campo di prova", "data_type": "string"}, http.StatusCreated)
	adm.do("PUT", "/api/v1/fields/campo_prova/types/business_request", map[string]any{"required": false}, http.StatusOK)
	adm.do("POST", "/api/v1/types", map[string]any{"key": "tipo_prova", "name": "Tipo di prova", "module": "work"}, http.StatusCreated)
	adm.do("PATCH", "/api/v1/types/business_request", map[string]any{"name": "Richiesta rinominata nella prova"}, http.StatusOK)
	adm.do("POST", "/api/v1/governance/bodies", map[string]any{"name": "Comitato di prova", "kind": "steering", "chair_id": sbxMember}, http.StatusCreated)
	adm.do("PUT", "/api/v1/devsecops/gate/policies/ambiente-prova", map[string]any{"mode": "warn", "rules": []any{}, "override_enabled": true, "override_hours": 12}, http.StatusOK)

	// Il principale non è cambiato.
	adm.do("POST", "/api/v1/me/tenant", map[string]any{"tenant_id": cr.TenantID}, http.StatusOK)
	adm.do("GET", "/api/v1/fields/campo_prova", nil, http.StatusNotFound)
	before := adm.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK)
	nv := len(list(before["versions"]))

	// Diff: il campo è nuovo, il processo è modificato; nient'altro.
	diff := adm.do("GET", "/api/v1/prova/differenze", nil, http.StatusOK)
	got := map[string]string{}
	for _, it := range list(diff["items"]) {
		o := obj(it)
		got[o["kind"].(string)+"/"+o["key"].(string)] = o["change"].(string)
	}
	if len(got) != 6 || got["field/campo_prova"] != "added" || got["process/demand_standard"] != "modified" || got["type/tipo_prova"] != "added" ||
		got["type/business_request"] != "modified" || got["gate/ambiente-prova"] != "added" {
		t.Fatalf("differenze: %v", got)
	}
	for _, it := range list(diff["items"]) {
		if o := obj(it); o["kind"] == "process" && (o["before_def"] == nil || o["after_def"] == nil || o["conflict"] != false) {
			t.Fatalf("processo senza le due definizioni: %v", o)
		}
	}

	// Promozione selettiva: solo il campo. Le scelte fuori dal confronto sono rifiutate.
	adm.do("POST", "/api/v1/prova/promuovi", map[string]any{"items": []any{}}, http.StatusUnprocessableEntity)
	adm.do("POST", "/api/v1/prova/promuovi", map[string]any{"items": []any{map[string]any{"kind": "field", "key": "non_esiste"}}}, http.StatusUnprocessableEntity)
	adm.do("POST", "/api/v1/prova/promuovi", map[string]any{"items": []any{map[string]any{"kind": "field", "key": "campo_prova"}}}, http.StatusOK)
	adm.do("GET", "/api/v1/fields/campo_prova", nil, http.StatusOK)
	if v := list(adm.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK)["versions"]); len(v) != nv {
		t.Fatalf("il processo non andava promosso: %d versioni, erano %d", len(v), nv)
	}
	diff = adm.do("GET", "/api/v1/prova/differenze", nil, http.StatusOK)
	if its := list(diff["items"]); len(its) != 5 {
		t.Fatalf("dopo la promozione del campo restano gli altri cinque: %v", its)
	}

	// Il tipo cambiato porta con sé il campo associato; il tipo nuovo, il gate e l'organo vengono con la loro promozione.
	diff = adm.do("GET", "/api/v1/prova/differenze", nil, http.StatusOK)
	var pick []any
	for _, it := range list(diff["items"]) {
		if o := obj(it); o["kind"] != "process" {
			pick = append(pick, map[string]any{"kind": o["kind"], "key": o["key"]})
		}
	}
	if len(pick) != 4 {
		t.Fatalf("da promuovere: %v", pick)
	}
	adm.do("POST", "/api/v1/prova/promuovi", map[string]any{"items": pick}, http.StatusOK)
	adm.do("GET", "/api/v1/types/tipo_prova", nil, http.StatusOK)
	if n := count("select count(*) from element_types where tenant_id = $1 and key = 'business_request' and name = 'Richiesta rinominata nella prova'", cr.TenantID); n != 1 {
		t.Fatal("il tipo modificato non è stato promosso")
	}
	if n := count("select count(*) from governance_bodies where tenant_id = $1 and name = 'Comitato di prova'", cr.TenantID); n != 1 {
		t.Fatal("l'organo non è stato promosso")
	}
	if n := count("select count(*) from gate_policies where tenant_id = $1 and environment = 'ambiente-prova' and override_hours = 12", cr.TenantID); n != 1 {
		t.Fatal("il gate non è stato promosso")
	}
	if n := count("select count(*) from type_fields tf join field_defs f on f.id = tf.field_id and f.tenant_id = tf.tenant_id join element_types t on t.id = tf.type_id and t.tenant_id = tf.tenant_id where tf.tenant_id = $1 and f.key = 'campo_prova' and t.key = 'business_request'", cr.TenantID); n != 1 {
		t.Fatal("il campo del tipo non è stato promosso")
	}
	diff = adm.do("GET", "/api/v1/prova/differenze", nil, http.StatusOK)
	if its := list(diff["items"]); len(its) != 1 || obj(its[0])["kind"] != "process" {
		t.Fatalf("resta solo il processo: %v", its)
	}

	// Poi il processo: diventa una versione nuova pubblicata, con la modifica.
	adm.do("POST", "/api/v1/prova/promuovi", map[string]any{"items": []any{map[string]any{"kind": "process", "key": "demand_standard"}}}, http.StatusOK)
	after := adm.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK)
	if len(list(after["versions"])) != nv+1 || obj(list(obj(after["current"])["phases"])[0])["description"] != "Cambiata nella prova" {
		t.Fatalf("processo promosso: %v", after["versions"])
	}
	if diff := adm.do("GET", "/api/v1/prova/differenze", nil, http.StatusOK); len(list(diff["items"])) != 0 {
		t.Fatalf("dopo la promozione non resta niente: %v", diff["items"])
	}

	// Audit in entrambi i tenant e storico nel principale.
	if n := count("select count(*) from audit_log where tenant_id = $1 and action = 'sandbox.promote'", cr.TenantID); n != 3 {
		t.Fatalf("audit del principale: %d promozioni", n)
	}
	if n := count("select count(*) from audit_log where tenant_id = $1 and action = 'sandbox.promote'", sbxID); n != 3 {
		t.Fatalf("audit della prova: %d promozioni", n)
	}
	if n := count("select count(*) from audit_log where tenant_id = $1 and action = 'sandbox.create'", cr.TenantID); n != 1 {
		t.Fatalf("audit della creazione: %d", n)
	}
	if h := list(adm.do("GET", "/api/v1/prova/promozioni", nil, http.StatusOK)["items"]); len(h) != 3 {
		t.Fatalf("storico: %v", h)
	}
	if n := count("select count(*) from ambiente_prova_promozioni where tenant_id = $1", sbxID); n != 0 {
		t.Fatal("lo storico va nel principale")
	}

	// Ricrea: la prova riparte dal principale (che ora ha già tutto) e il confronto resta vuoto; un
	// cambiamento fatto nella prova e non promosso si perde.
	adm.do("POST", "/api/v1/me/tenant", map[string]any{"tenant_id": sbxID}, http.StatusOK)
	adm.do("POST", "/api/v1/fields", map[string]any{"key": "campo_perso", "name": "Campo perso", "data_type": "string"}, http.StatusCreated)
	adm.do("POST", "/api/v1/prova/ricrea", nil, http.StatusOK)
	adm.do("GET", "/api/v1/fields/campo_perso", nil, http.StatusNotFound)
	adm.do("GET", "/api/v1/fields/campo_prova", nil, http.StatusOK)
	if diff := adm.do("GET", "/api/v1/prova/differenze", nil, http.StatusOK); len(list(diff["items"])) != 0 {
		t.Fatalf("dopo «ricrea» non ci sono differenze: %v", diff["items"])
	}
	// Nel principale, dopo aver ricreato, non c'è niente della prova.
	adm.do("POST", "/api/v1/me/tenant", map[string]any{"tenant_id": cr.TenantID}, http.StatusOK)
	adm.do("GET", "/api/v1/fields/campo_perso", nil, http.StatusNotFound)
}
