package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
	"regia/internal/platform/tenant"
)

// Ambiente di prova e promuovi (usabilità, ondata 3; migrazione 0065; docs/sviluppo/ambiente-prova.md).
//
//	GET  /api/v1/prova               stato: nessuna prova, principale con la sua prova, oppure «sono nella prova»
//	POST /api/v1/prova               crea la prova collegata (dal principale), con o senza campione di elementi
//	POST /api/v1/prova/ricrea        riallinea la prova dal principale (dall'uno o dall'altra)
//	GET  /api/v1/prova/differenze    cosa cambia per oggetto: aggiunto, modificato, tolto
//	POST /api/v1/prova/promuovi      applica nel principale gli oggetti scelti, in una transazione
//	GET  /api/v1/prova/promozioni    storico delle promozioni
//
// Creare, ricreare, confrontare e promuovere richiedono tenant.admin su entrambi i tenant.
func (s *Server) provaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/prova", inTenant(s.provaState))
	mux.HandleFunc("POST /api/v1/prova", can(rbac.TenantAdmin, s.provaCreate))
	mux.HandleFunc("POST /api/v1/prova/ricrea", can(rbac.TenantAdmin, s.provaRecreate))
	mux.HandleFunc("GET /api/v1/prova/differenze", can(rbac.TenantAdmin, s.provaDiff))
	mux.HandleFunc("POST /api/v1/prova/promuovi", can(rbac.TenantAdmin, s.provaPromote))
	mux.HandleFunc("GET /api/v1/prova/promozioni", can(rbac.TenantAdmin, s.provaHistory))
}

var (
	dbRO = db.TenantOpts{ReadOnly: true}
	dbRW = db.TenantOpts{}
)

// provaEnv è la coppia principale/prova vista dal tenant attivo, con i permessi verificati su entrambi.
type provaEnv struct {
	Role           string // del tenant attivo: principale | prova
	MainID, SbxID  string
	MainName       string
	SbxName        string
	WithSample     bool
	Baseline       map[string]string
	Member         map[string]string       // tenant → membro di chi agisce
	Actor          map[string]engine.Actor // tenant → attore con i permessi che ha lì
	SbxAdminMember string
}

type provaLink struct {
	Role        string
	Linked      string
	WithSample  bool
	CreatedAt   time.Time
	RefreshedAt time.Time
	Baseline    map[string]string
}

func readLink(ctx context.Context, tx pgx.Tx) (*provaLink, error) {
	l := &provaLink{}
	err := tx.QueryRow(ctx, `select role, linked_tenant_id, with_sample, created_at, refreshed_at, baseline from ambiente_prova`).
		Scan(&l.Role, &l.Linked, &l.WithSample, &l.CreatedAt, &l.RefreshedAt, &l.Baseline)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if l.Baseline == nil {
		l.Baseline = map[string]string{}
	}
	return l, err
}

// permsIn legge i permessi di un membro in un tenant (come auth.loadTenant).
func permsIn(ctx context.Context, tx pgx.Tx, tenantID, memberID string) ([]string, error) {
	if err := bindTo(ctx, tx, tenantID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select distinct unnest(r.permissions) from role_assignments ra
		join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
		where ra.space_id is null and (ra.member_id = $1 or ra.group_id in (select member_groups($1)))`, memberID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func isAdmin(perms []string) bool {
	return slices.Contains(perms, "*") || slices.Contains(perms, rbac.TenantAdmin)
}

// provaEnv risolve la coppia dal tenant attivo e verifica tenant.admin su entrambi. Lascia la
// transazione legata al tenant attivo.
func (s *Server) provaEnv(ctx context.Context, tx pgx.Tx, p *auth.Principal, ip netip.Addr) (*provaEnv, error) {
	if err := bindTo(ctx, tx, p.TenantID); err != nil {
		return nil, err
	}
	link, err := readLink(ctx, tx)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, engine.InvalidCode("validation.sandbox.not_linked", "prova", "non c'è nessun ambiente di prova collegato")
	}
	env := &provaEnv{Role: link.Role, Member: map[string]string{}, Actor: map[string]engine.Actor{}}
	if link.Role == "principale" {
		env.MainID, env.SbxID = p.TenantID, link.Linked
	} else {
		env.MainID, env.SbxID = link.Linked, p.TenantID
	}
	for _, id := range []string{env.MainID, env.SbxID} {
		var found bool
		for _, m := range p.Tenants {
			// Un'organizzazione sospesa non si usa, neanche come ambiente di prova collegato.
			if m.TenantID != id || !m.Active() {
				continue
			}
			found = true
			env.Member[id] = m.MemberID
			if id == env.MainID {
				env.MainName = m.Name
			} else {
				env.SbxName = m.Name
			}
		}
		if !found {
			return nil, engine.ErrForbidden
		}
		perms, err := permsIn(ctx, tx, id, env.Member[id])
		if err != nil {
			return nil, err
		}
		if !isAdmin(perms) {
			return nil, engine.ErrForbidden
		}
		env.Actor[id] = engine.Actor{AccountID: p.AccountID, MemberID: env.Member[id], Permissions: perms, IP: ip}
	}
	env.SbxAdminMember = env.Member[env.SbxID]
	// La baseline sta nella riga della prova.
	if err := bindTo(ctx, tx, env.SbxID); err != nil {
		return nil, err
	}
	sl, err := readLink(ctx, tx)
	if err != nil {
		return nil, err
	}
	if sl != nil {
		env.Baseline, env.WithSample = sl.Baseline, sl.WithSample
	}
	return env, bindTo(ctx, tx, p.TenantID)
}

func (s *Server) provaAudit(ctx context.Context, tx pgx.Tx, env *provaEnv, tenantID, action string, p *auth.Principal, ip netip.Addr, payload map[string]any) error {
	if err := bindTo(ctx, tx, tenantID); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "sandbox", EntityID: tenantID, Payload: payload,
		ActorAccountID: p.AccountID, ActorMemberID: env.Member[tenantID], IP: ip})
}

// ---------------------------------------------------------------------
// Stato
// ---------------------------------------------------------------------

type provaState struct {
	// Role: none | principale | prova.
	Role        string     `json:"role"`
	Linked      *provaPeer `json:"linked,omitempty"`
	WithSample  bool       `json:"with_sample"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	RefreshedAt *time.Time `json:"refreshed_at,omitempty"`
	// CanManage: l'utente può creare e promuovere (tenant.admin qui).
	CanManage bool `json:"can_manage"`
}

type provaPeer struct {
	TenantID string `json:"tenant_id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
}

func (s *Server) provaState(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*provaState, error) {
		out := &provaState{Role: "none", CanManage: p.Can(rbac.TenantAdmin)}
		l, err := readLink(r.Context(), tx)
		if err != nil || l == nil {
			return out, err
		}
		out.Role, out.WithSample, out.CreatedAt, out.RefreshedAt = l.Role, l.WithSample, &l.CreatedAt, &l.RefreshedAt
		for _, m := range p.Tenants {
			if m.TenantID == l.Linked {
				out.Linked = &provaPeer{TenantID: m.TenantID, Slug: m.Slug, Name: m.Name}
			}
		}
		if out.Linked == nil {
			out.Linked = &provaPeer{TenantID: l.Linked}
		}
		return out, nil
	})
}

// ---------------------------------------------------------------------
// Crea e ricrea
// ---------------------------------------------------------------------

func (s *Server) provaCreate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Sample bool `json:"sample"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	var mainSlug, mainName string
	for _, m := range p.Tenants {
		if m.TenantID == p.TenantID {
			mainSlug, mainName = m.Slug, m.Name
		}
	}
	// Controlli preliminari, senza scrivere.
	var lang, currency string
	err := s.db.InTenant(ctx, p.TenantID, dbRO, func(tx pgx.Tx) error {
		l, err := readLink(ctx, tx)
		if err != nil {
			return err
		}
		if l != nil && l.Role == "prova" {
			return engine.InvalidCode("validation.sandbox.in_sandbox", "prova", "questo è già un ambiente di prova")
		}
		if l != nil {
			return engine.InvalidCode("validation.sandbox.exists", "prova", "esiste già un ambiente di prova collegato")
		}
		_ = tx.QueryRow(ctx, "select locale from tenant_language").Scan(&lang)
		return nil
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	if err := s.db.Owner.QueryRow(ctx, "select base_currency from tenants where id = $1", p.TenantID).Scan(&currency); err != nil {
		s.internal(w, r, err)
		return
	}
	base := mainSlug
	if len(base) > 57 {
		base = strings.TrimRight(base[:57], "-")
	}
	created, err := tenant.Create(ctx, s.db, tenant.CreateInput{Slug: base + "-prova", Name: mainName + " (prova)", AdminEmail: p.Email,
		AdminName: p.DisplayName, BaseCurrency: currency, Language: lang})
	if errors.Is(err, tenant.ErrSlugTaken) {
		s.engineError(w, r, engine.InvalidCode("validation.sandbox.slug_taken", "prova", "esiste già un tenant con l'identificativo della prova"))
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	ip := clientIP(r)
	err = s.db.InTenant(ctx, p.TenantID, dbRW, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into ambiente_prova (tenant_id, role, linked_tenant_id, with_sample, created_by)
			values (app_tenant(), 'principale', $1, $2, $3)`, created.TenantID, in.Sample, p.MemberID); err != nil {
			return err
		}
		if err := copyConfig(ctx, tx, p.TenantID, created.TenantID, created.AdminMemberID, in.Sample); err != nil {
			return err
		}
		snap, err := snapshot(ctx, tx) // legato alla prova da copyConfig
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into ambiente_prova (tenant_id, role, linked_tenant_id, with_sample, created_by, baseline)
			values (app_tenant(), 'prova', $1, $2, $3, $4)`, p.TenantID, in.Sample, created.AdminMemberID, baselineOf(snap)); err != nil {
			return err
		}
		env := &provaEnv{Member: map[string]string{p.TenantID: p.MemberID, created.TenantID: created.AdminMemberID}}
		pl := map[string]any{"sandbox_tenant_id": created.TenantID, "sample": in.Sample, "objects": len(snap)}
		if err := s.provaAudit(ctx, tx, env, created.TenantID, "sandbox.create", p, ip, map[string]any{"main_tenant_id": p.TenantID, "sample": in.Sample}); err != nil {
			return err
		}
		return s.provaAudit(ctx, tx, env, p.TenantID, "sandbox.create", p, ip, pl)
	})
	if err != nil {
		// Si toglie il tenant appena creato: senza collegamento non servirebbe a niente.
		_, _ = s.db.Owner.Exec(context.WithoutCancel(ctx), "delete from tenants where id = $1", created.TenantID)
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"tenant_id": created.TenantID, "slug": base + "-prova"})
}

func baselineOf(snap pSnap) map[string]string {
	out := make(map[string]string, len(snap))
	for k, o := range snap {
		out[k] = o.Hash
	}
	return out
}

func (s *Server) provaRecreate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	ctx := r.Context()
	ip := clientIP(r)
	var out map[string]any
	err := s.db.InTenant(ctx, p.TenantID, dbRW, func(tx pgx.Tx) error {
		env, err := s.provaEnv(ctx, tx, p, ip)
		if err != nil {
			return err
		}
		if err := clearConfig(ctx, tx, env.SbxID); err != nil {
			return err
		}
		if err := copyConfig(ctx, tx, env.MainID, env.SbxID, env.SbxAdminMember, env.WithSample); err != nil {
			return err
		}
		snap, err := snapshot(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update ambiente_prova set baseline = $1, refreshed_at = now()", baselineOf(snap)); err != nil {
			return err
		}
		if err := bindTo(ctx, tx, env.MainID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update ambiente_prova set refreshed_at = now()"); err != nil {
			return err
		}
		pl := map[string]any{"sandbox_tenant_id": env.SbxID, "objects": len(snap)}
		if err := s.provaAudit(ctx, tx, env, env.SbxID, "sandbox.recreate", p, ip, map[string]any{"main_tenant_id": env.MainID}); err != nil {
			return err
		}
		if err := s.provaAudit(ctx, tx, env, env.MainID, "sandbox.recreate", p, ip, pl); err != nil {
			return err
		}
		out = map[string]any{"objects": len(snap)}
		return bindTo(ctx, tx, p.TenantID)
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------
// Confronto, promozione, storico
// ---------------------------------------------------------------------

type provaDiffOut struct {
	MainName string      `json:"main_name"`
	SbxName  string      `json:"sandbox_name"`
	Items    []provaItem `json:"items"`
}

// diffNow calcola le differenze correnti (la transazione finisce legata al tenant attivo).
func (s *Server) diffNow(ctx context.Context, tx pgx.Tx, env *provaEnv) ([]provaItem, pSnap, error) {
	if err := bindTo(ctx, tx, env.MainID); err != nil {
		return nil, nil, err
	}
	main, err := snapshot(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	if err := bindTo(ctx, tx, env.SbxID); err != nil {
		return nil, nil, err
	}
	sbx, err := snapshot(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	return computeDiff(main, sbx, env.Baseline), sbx, nil
}

func (s *Server) provaDiff(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*provaDiffOut, error) {
		ctx := r.Context()
		env, err := s.provaEnv(ctx, tx, p, clientIP(r))
		if err != nil {
			return nil, err
		}
		items, _, err := s.diffNow(ctx, tx, env)
		if err != nil {
			return nil, err
		}
		return &provaDiffOut{MainName: env.MainName, SbxName: env.SbxName, Items: items}, bindTo(ctx, tx, p.TenantID)
	})
}

type provaPick struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

func (s *Server) provaPromote(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Items []provaPick `json:"items"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if len(in.Items) == 0 {
		s.engineError(w, r, engine.InvalidCode("validation.sandbox.nothing_selected", "items", "scegli almeno un oggetto da promuovere"))
		return
	}
	ctx := r.Context()
	ip := clientIP(r)
	var promoted []provaItem
	err := s.db.InTenant(ctx, p.TenantID, dbRW, func(tx pgx.Tx) error {
		env, err := s.provaEnv(ctx, tx, p, ip)
		if err != nil {
			return err
		}
		items, sbx, err := s.diffNow(ctx, tx, env)
		if err != nil {
			return err
		}
		byKey := map[string]provaItem{}
		for _, it := range items {
			byKey[snapKey(it.Kind, it.Key)] = it
		}
		picked := map[string]provaItem{}
		for _, pk := range in.Items {
			it, ok := byKey[snapKey(pk.Kind, pk.Key)]
			if !ok {
				return engine.InvalidCode("validation.sandbox.item_unknown", "items", "«%s» non è tra le differenze: ricarica il confronto", pk.Key)
			}
			picked[snapKey(pk.Kind, pk.Key)] = it
		}
		pm := &promoter{s: s, env: env, sbx: sbx, actorMain: env.Actor[env.MainID], actorSbx: env.Actor[env.SbxID],
			remap: remapper{env.SbxID: env.MainID, env.SbxAdminMember: env.Member[env.MainID]}}
		for _, kind := range provaKinds {
			for _, it := range items {
				if it.Kind != kind {
					continue
				}
				if _, ok := picked[snapKey(it.Kind, it.Key)]; !ok {
					continue
				}
				if err := pm.apply(ctx, tx, it); err != nil {
					return err
				}
				promoted = append(promoted, it)
			}
		}
		// La baseline avanza per gli oggetti promossi: da ora il principale li ha uguali alla prova.
		base := env.Baseline
		for _, it := range promoted {
			k := snapKey(it.Kind, it.Key)
			if o := sbx[k]; o != nil {
				base[k] = o.Hash
			} else {
				delete(base, k)
			}
		}
		if err := bindTo(ctx, tx, env.SbxID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update ambiente_prova set baseline = $1", base); err != nil {
			return err
		}
		list := make([]map[string]string, 0, len(promoted))
		summary := map[string]int{}
		for _, it := range promoted {
			list = append(list, map[string]string{"kind": it.Kind, "key": it.Key, "name": it.Name, "change": it.Change})
			summary[it.Change]++
		}
		if err := bindTo(ctx, tx, env.MainID); err != nil {
			return err
		}
		raw, _ := json.Marshal(list)
		if _, err := tx.Exec(ctx, `insert into ambiente_prova_promozioni (tenant_id, sandbox_tenant_id, promoted_by, items, summary)
			values (app_tenant(), $1, $2, $3, $4)`, env.SbxID, env.Member[env.MainID], raw, summary); err != nil {
			return err
		}
		if err := s.provaAudit(ctx, tx, env, env.MainID, "sandbox.promote", p, ip, map[string]any{"sandbox_tenant_id": env.SbxID, "items": list}); err != nil {
			return err
		}
		if err := s.provaAudit(ctx, tx, env, env.SbxID, "sandbox.promote", p, ip, map[string]any{"main_tenant_id": env.MainID, "items": list}); err != nil {
			return err
		}
		return bindTo(ctx, tx, p.TenantID)
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"promoted": promoted})
}

func (s *Server) provaHistory(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (map[string]any, error) {
		ctx := r.Context()
		env, err := s.provaEnv(ctx, tx, p, clientIP(r))
		if err != nil {
			return nil, err
		}
		if err := bindTo(ctx, tx, env.MainID); err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, `select h.id, h.promoted_at, coalesce(m.display_name, ''), h.items, h.summary
			from ambiente_prova_promozioni h left join members m on m.tenant_id = h.tenant_id and m.id = h.promoted_by
			order by h.promoted_at desc limit 50`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, by string
			var at time.Time
			var list, summary any
			if err := rows.Scan(&id, &at, &by, &list, &summary); err != nil {
				return nil, err
			}
			items = append(items, map[string]any{"id": id, "promoted_at": at, "promoted_by": by, "items": list, "summary": summary})
		}
		rows.Close()
		return map[string]any{"items": items}, bindTo(ctx, tx, p.TenantID)
	})
}
