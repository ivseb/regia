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

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

func c4Server(t *testing.T) *httptest.Server {
	t.Helper()
	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: needDB(t), Auth: auth.NewService(needDB(t), time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	t.Cleanup(srv.Close)
	return srv
}

// Permessi lungo la gerarchia dei gruppi: il ruolo del gruppo padre vale per i membri del
// figlio, l'opzione del tenant lo spegne, la spiegazione mostra la catena; i gruppi negli
// organi di governance danno membri effettivi, quorum e presenze calcolati alla seduta.
func TestPermessiGerarchiaGruppiEGovernance(t *testing.T) {
	srv := c4Server(t)
	e := newEAEnv(t, srv.URL, "gh")
	admin, view := e.admin, e.view
	const base = "/api/v1/architecture"
	rel := map[string]any{"type": "calls_api", "source": "A", "target": "C"}

	// Gruppi: Dipartimento > Squadra. Il lettore sta solo nella Squadra.
	parent := admin.do("POST", "/api/v1/admin/groups", map[string]any{"name": "Dipartimento IT"}, http.StatusCreated)["id"].(string)
	child := admin.do("POST", "/api/v1/admin/groups", map[string]any{"name": "Squadra canali", "parent_id": parent}, http.StatusCreated)["id"].(string)
	admin.do("POST", "/api/v1/admin/groups/"+child+"/members", map[string]any{"member_id": view.member}, http.StatusNoContent)
	var architectRole string
	for _, it := range admin.do("GET", "/api/v1/admin/roles", nil, http.StatusOK)["items"].([]any) {
		if m := it.(map[string]any); m["key"] == "architect" {
			architectRole = m["id"].(string)
		}
	}
	admin.do("POST", "/api/v1/admin/roles/"+architectRole+"/assignments", map[string]any{"group_id": parent}, http.StatusCreated)

	// Opzione attiva per default: il ruolo del padre vale per il membro del figlio.
	if sec := admin.do("GET", "/api/v1/admin/security", nil, http.StatusOK); sec["group_inheritance"] != true {
		t.Fatalf("opzione di default: %v", sec)
	}
	view.do("POST", base+"/relations", rel, http.StatusCreated)

	// La spiegazione mostra la catena gruppo -> ruolo -> permesso.
	ex := admin.do("GET", "/api/v1/admin/members/"+view.member+"/access", nil, http.StatusOK)
	var found bool
	for _, g := range eaList(ex["grants"]) {
		if g["source"] == "inherited" && g["role"] == "Architetto" {
			chain := g["chain"].([]any)
			if len(chain) != 2 || chain[0] != "Squadra canali" || chain[1] != "Dipartimento IT" {
				t.Fatalf("catena: %v", chain)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("spiegazione senza il ruolo ereditato: %v", ex)
	}
	var hasWrite bool
	for _, p := range eaList(ex["permissions"]) {
		hasWrite = hasWrite || p["permission"] == "architecture.write"
	}
	if !hasWrite {
		t.Fatalf("permesso mancante nella spiegazione: %v", ex["permissions"])
	}

	// Spenta, il ruolo del padre non vale più per il figlio; riaccesa, torna.
	admin.do("PUT", "/api/v1/admin/security", map[string]any{"group_inheritance": false}, http.StatusOK)
	view.do("DELETE", base+"/relations/"+"00000000-0000-0000-0000-000000000000", nil, http.StatusForbidden)
	ex = admin.do("GET", "/api/v1/admin/members/"+view.member+"/access", nil, http.StatusOK)
	if ex["inheritance"] != false || len(eaList(ex["grants"])) != 1 { // resta il ruolo "Lettore"
		t.Fatalf("senza ereditarietà: %v", ex)
	}
	admin.do("PUT", "/api/v1/admin/security", map[string]any{"group_inheritance": true}, http.StatusOK)

	// Governance: l'organo ha il gruppo padre; il membro del figlio siede e conta nel quorum.
	body := admin.do("POST", "/api/v1/governance/bodies", map[string]any{
		"name": "Board canali", "kind": "other", "chair_id": admin.member, "quorum": 2,
		"groups": []map[string]any{{"group_id": parent}},
	}, http.StatusCreated)
	eff := eaList(body["effective"])
	if len(eff) != 2 || num(body["quorum_required"]) != 2 {
		t.Fatalf("membri effettivi: %v", body)
	}
	var via any
	for _, m := range eff {
		if m["member_id"] == view.member {
			via = m["via_group"]
		}
	}
	if via != "Dipartimento IT" {
		t.Fatalf("il lettore deve arrivare dal gruppo: %v", eff)
	}
	if len(eaList(body["members"])) != 1 {
		t.Fatalf("i membri diretti restano solo il presidente: %v", body["members"])
	}
	// Un quorum oltre i votanti effettivi si rifiuta.
	admin.do("PATCH", "/api/v1/governance/bodies/"+body["id"].(string), map[string]any{
		"name": "Board canali", "kind": "other", "chair_id": admin.member, "quorum": 5,
	}, http.StatusUnprocessableEntity)
	// Senza "groups" i gruppi restano come sono.
	upd := admin.do("PATCH", "/api/v1/governance/bodies/"+body["id"].(string), map[string]any{
		"name": "Board canali 2", "kind": "other", "chair_id": admin.member,
	}, http.StatusOK)
	if len(eaList(upd["groups"])) != 1 {
		t.Fatalf("gruppi persi: %v", upd["groups"])
	}
	m := admin.do("POST", "/api/v1/governance/meetings", map[string]any{"body_id": body["id"], "scheduled_at": "2027-03-10T10:00:00+01:00", "propose": false}, http.StatusCreated)
	if len(eaList(m["attendance"])) != 2 {
		t.Fatalf("presenze alla creazione: %v", m["attendance"])
	}
	// Un nuovo membro del gruppo prima della convocazione entra tra i presenti.
	extra := e.dm
	admin.do("POST", "/api/v1/admin/groups/"+child+"/members", map[string]any{"member_id": extra.member}, http.StatusNoContent)
	c := admin.do("POST", "/api/v1/governance/meetings/"+m["id"].(string)+"/status", map[string]any{"status": "convened"}, http.StatusOK)
	if len(eaList(c["attendance"])) != 3 || num(c["quorum"].(map[string]any)["voting"]) != 3 {
		t.Fatalf("presenze alla convocazione: %v", c["attendance"])
	}
}

// Diagrammi C4 generati dal grafo: contesto, container e componenti; seguono i dati.
func TestDiagrammiC4(t *testing.T) {
	srv := c4Server(t)
	e := newEAEnv(t, srv.URL, "c4")
	arch, view := e.arch, e.view
	const base = "/api/v1/architecture"
	for _, r := range [][3]string{{"calls_api", "A", "B"}, {"reads_data_from", "B", "C"}, {"sends_events_to", "E", "B"}} {
		arch.do("POST", base+"/relations", map[string]any{"type": r[0], "source": r[1], "target": r[2],
			"attrs": map[string]any{"protocol": "REST", "data": "saldi"}}, http.StatusCreated)
	}
	var assetID string
	for _, a := range eaItems(arch.do("GET", base+"/assets", nil, http.StatusOK)) {
		if a["name"] == "Canali digitali" {
			assetID = a["id"].(string)
		}
	}
	if assetID == "" {
		t.Fatal("asset Canali digitali non trovato")
	}

	// Contesto di B: A, C, E sono sistemi esterni, più gli attori.
	view.do("POST", base+"/diagrams/c4", map[string]any{"root_id": e.id("B"), "level": "context"}, http.StatusForbidden)
	ctx1 := arch.do("POST", base+"/diagrams/c4", map[string]any{"root_id": e.id("B"), "level": "context"}, http.StatusOK)
	c4 := ctx1["c4"].(map[string]any)
	kinds := map[string]int{}
	for _, n := range eaList(c4["nodes"]) {
		kinds[n["kind"].(string)]++
	}
	if kinds["system"] != 1 || kinds["external"] != 3 || kinds["person"] != 1 || len(eaList(c4["edges"])) != 4 {
		t.Fatalf("contesto: %v", kinds)
	}
	for _, ed := range eaList(c4["edges"]) {
		if ed["label"] != "Usa" && ed["protocol"] != "REST" {
			t.Fatalf("freccia senza protocollo: %v", ed)
		}
	}
	// Rigenerare restituisce lo stesso diagramma.
	again := arch.do("POST", base+"/diagrams/c4", map[string]any{"root_id": e.id("B"), "level": "context"}, http.StatusOK)
	if again["id"] != ctx1["id"] {
		t.Fatal("la rigenerazione ha duplicato il diagramma")
	}
	// Container: l'applicazione con i database della tecnologia e l'asset di cui fa parte.
	tech := arch.do("POST", base+"/technologies", map[string]any{"name": "PostgreSQL", "version": "16", "category": "Database"}, http.StatusCreated)
	arch.do("PUT", base+"/applications/"+e.id("B")+"/technologies", map[string]any{"technology_ids": []string{tech["id"].(string)}}, http.StatusNoContent)
	cont := arch.do("POST", base+"/diagrams/c4", map[string]any{"root_id": e.id("B"), "level": "container"}, http.StatusOK)
	ck := map[string]int{}
	for _, n := range eaList(cont["c4"].(map[string]any)["nodes"]) {
		ck[n["kind"].(string)]++
	}
	if ck["database"] != 1 || ck["asset"] != 1 || ck["container"] != 1 || ck["external"] != 3 {
		t.Fatalf("container: %v", ck)
	}
	// L'asset non ha un diagramma di container; l'applicazione non ha componenti.
	arch.do("POST", base+"/diagrams/c4", map[string]any{"root_id": assetID, "level": "container"}, http.StatusUnprocessableEntity)
	arch.do("POST", base+"/diagrams/c4", map[string]any{"root_id": e.id("B"), "level": "component"}, http.StatusUnprocessableEntity)
	// Componenti dell'asset: i moduli A e B, con la dipendenza tra loro.
	comp := arch.do("POST", base+"/diagrams/c4", map[string]any{"root_id": assetID, "level": "component"}, http.StatusOK)
	cm := comp["c4"].(map[string]any)
	if cm["boundary"] != "Canali digitali" {
		t.Fatalf("confine: %v", cm["boundary"])
	}
	cc := map[string]int{}
	for _, n := range eaList(cm["nodes"]) {
		cc[n["kind"].(string)]++
	}
	if cc["component"] != 2 || cc["external"] != 2 { // C ed E
		t.Fatalf("componenti: %v", cc)
	}

	// Il diagramma segue i dati: una nuova dipendenza compare senza toccare il diagramma.
	arch.do("POST", base+"/relations", map[string]any{"type": "calls_api", "source": "B", "target": "D"}, http.StatusCreated)
	after := arch.do("GET", base+"/diagrams/"+ctx1["id"].(string), nil, http.StatusOK)
	if n := len(eaList(after["c4"].(map[string]any)["nodes"])); n != len(eaList(c4["nodes"]))+1 {
		t.Fatalf("il diagramma non segue i dati: %d nodi", n)
	}

	// Posizioni e attori si salvano nel layout; l'attore nominato segue il membro.
	layout := after["layout"].(map[string]any)
	spec := layout["c4"].(map[string]any)
	spec["actors"] = []any{map[string]any{"id": "ruolo1", "name": "Gestore canali", "kind": "role", "label": "Gestisce"},
		map[string]any{"id": "pers1", "name": "ignorato", "kind": "person", "member_id": view.member}}
	layout["pos"] = map[string]any{"e:" + e.id("B"): map[string]any{"x": 120, "y": 80}}
	saved := arch.do("PUT", base+"/diagrams/"+ctx1["id"].(string), map[string]any{"layout": layout, "version": after["version"]}, http.StatusOK)
	var names []string
	for _, n := range eaList(saved["c4"].(map[string]any)["nodes"]) {
		if n["kind"] == "person" {
			names = append(names, n["name"].(string))
		}
	}
	if strings.Join(names, ",") != "Gestore canali,view" {
		t.Fatalf("attori: %v", names)
	}
	if p := saved["layout"].(map[string]any)["pos"].(map[string]any); len(p) != 1 {
		t.Fatalf("posizioni: %v", p)
	}
	// Una chiave nascosta sparisce dal modello.
	spec2 := saved["layout"].(map[string]any)
	spec2["c4"].(map[string]any)["hidden"] = []any{"e:" + e.id("D")}
	hid := arch.do("PUT", base+"/diagrams/"+ctx1["id"].(string), map[string]any{"layout": spec2, "version": saved["version"]}, http.StatusOK)
	if n := len(eaList(hid["c4"].(map[string]any)["nodes"])); n != len(eaList(c4["nodes"]))+1 {
		t.Fatalf("nodo nascosto ancora presente: %d", n)
	}
}

// Costo del calcolo dei permessi (caricamento del principal a ogni richiesta): la query
// precedente, sui soli gruppi diretti, contro quella con la gerarchia. Albero di 400 gruppi
// su 6 livelli, 1000 membri, 150 ruoli assegnati ai gruppi.
func TestCostoCalcoloPermessiGerarchia(t *testing.T) {
	d := needDB(t)
	c := newTenant(t, d)
	ctx := context.Background()
	const oldQ = `select distinct unnest(r.permissions) from role_assignments ra
		join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
		where ra.space_id is null and (ra.member_id = $1 or ra.group_id in (select group_id from group_members where member_id = $1))`
	const newQ = `select distinct unnest(r.permissions) from role_assignments ra
		join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
		where ra.space_id is null and (ra.member_id = $1 or ra.group_id in (select member_groups($1)))`
	var member string
	err := d.InTenant(ctx, c.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		// 400 gruppi: 1 radice, poi ogni gruppo ha come padre uno dei livelli precedenti (profondità 6).
		if _, err := tx.Exec(ctx, `insert into groups (tenant_id, id, name, parent_id)
			select app_tenant(), ('00000000-0000-4000-8000-' || lpad(i::text, 12, '0'))::uuid, 'G' || i,
			       case when i = 1 then null else ('00000000-0000-4000-8000-' || lpad(((i - 2) / 3 + 1)::text, 12, '0'))::uuid end
			from generate_series(1, 400) i`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into members (tenant_id, id, display_name, member_type)
			select app_tenant(), ('10000000-0000-4000-8000-' || lpad(i::text, 12, '0'))::uuid, 'M' || i, 'standard' from generate_series(1, 1000) i`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into group_members (tenant_id, group_id, member_id)
			select app_tenant(), ('00000000-0000-4000-8000-' || lpad((100 + (i * k) % 300)::text, 12, '0'))::uuid, ('10000000-0000-4000-8000-' || lpad(i::text, 12, '0'))::uuid
			from generate_series(1, 1000) i, generate_series(1, 3) k on conflict do nothing`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into role_assignments (tenant_id, id, role_id, group_id)
			select app_tenant(), gen_random_uuid(), (select id from roles where key = 'viewer'), ('00000000-0000-4000-8000-' || lpad(i::text, 12, '0'))::uuid
			from generate_series(1, 150) i`); err != nil {
			return err
		}
		member = "10000000-0000-4000-8000-000000000007"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	measure := func(q string) time.Duration {
		var total time.Duration
		const n = 300
		err := d.InTenant(ctx, c.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
			for i := 0; i < n+20; i++ {
				start := time.Now()
				rows, err := tx.Query(ctx, q, member)
				if err != nil {
					return err
				}
				for rows.Next() {
				}
				rows.Close()
				if i >= 20 {
					total += time.Since(start)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return total / n
	}
	before, after := measure(oldQ), measure(newQ)
	t.Logf("calcolo dei permessi di un membro: prima %v, dopo (con gerarchia) %v", before, after)
	if after > 20*time.Millisecond {
		t.Fatalf("il calcolo con la gerarchia costa troppo: %v", after)
	}
}
