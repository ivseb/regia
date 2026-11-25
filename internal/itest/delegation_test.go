package itest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Con una delega valida il delegato vede il passo nell'inbox e può decidere;
// scaduta la delega, no. Lo sponsor resta sempre autorizzato.
func TestInboxDelegation(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	c := newTenant(t, d)
	var slug string
	if err := d.Owner.QueryRow(ctx, "select slug from tenants where id = $1", c.TenantID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	if err := tenant.Provision(ctx, d, slug, []tenant.Provisioner{demand.Provision, architecture.Provision}); err != nil {
		t.Fatal(err)
	}
	add := func(name string) string {
		id, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: name + "-" + slug + "@example.test", Password: testPassword, Roles: []string{"viewer"}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	sponsor, delegate := add("sponsor"), add("delegato")

	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	admin := engine.Actor{MemberID: c.AdminMemberID, Permissions: []string{"*"}}
	run := func(fn func(tx pgx.Tx) error) {
		t.Helper()
		if err := d.InTenant(ctx, c.TenantID, db.TenantOpts{}, fn); err != nil {
			t.Fatal(err)
		}
	}
	var elID string
	run(func(tx pgx.Tx) error {
		high := int64(1_000_000)
		el, err := dm.Create(ctx, tx, admin, demand.NewRequest{Title: "Richiesta delegata", SponsorID: sponsor, Data: demand.Data{EstimateHighCents: &high}})
		if err != nil {
			return err
		}
		elID = el.ID
		for _, to := range []string{"Inviata", "Triage", "Valutazione", "Prioritizzazione", "Approvazione"} {
			if _, err := eng.Transition(ctx, tx, admin, el.ID, to, "", engine.TransitionOpts{}); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `insert into delegations (tenant_id, from_member_id, to_member_id, valid_from, valid_to)
			values (app_tenant(), $1, $2, now() - interval '1 day', now() + interval '1 day')`, sponsor, delegate)
		return err
	})
	inbox := func(member string) []approval.InboxItem {
		var items []approval.InboxItem
		run(func(tx pgx.Tx) error {
			var err error
			items, err = ap.Inbox(ctx, tx, engine.Actor{MemberID: member, Permissions: []string{"element.read"}})
			return err
		})
		return items
	}
	if n := len(inbox(sponsor)); n != 1 {
		t.Fatalf("inbox dello sponsor: %d", n)
	}
	if n := len(inbox(delegate)); n != 1 {
		t.Fatalf("inbox del delegato con delega valida: %d", n)
	}
	run(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "update delegations set valid_to = now() - interval '1 hour', valid_from = now() - interval '2 days'")
		return err
	})
	if n := len(inbox(delegate)); n != 0 {
		t.Fatalf("inbox del delegato a delega scaduta: %d", n)
	}
	// Lo sponsor decide; la richiesta (10 k€) passa in Pianificazione.
	step := inbox(sponsor)[0].StepID
	run(func(tx pgx.Tx) error {
		return ap.Decide(ctx, tx, engine.Actor{MemberID: sponsor, Permissions: []string{"element.read"}}, step, approval.DecideInput{Decision: "approved"})
	})
	run(func(tx pgx.Tx) error {
		el, err := eng.Get(ctx, tx, admin, elID)
		if err != nil {
			return err
		}
		if el.State != "Pianificazione" {
			t.Fatalf("stato dopo l'approvazione: %s", el.State)
		}
		return nil
	})

	// fts_match gira fuori dalla RLS: deve restare confinata nel tenant della transazione.
	count := func(tenantID string) int {
		var n int
		fn := func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, "select count(*) from fts_match('delegata', 100)").Scan(&n)
		}
		if tenantID == "" {
			if err := d.Global(ctx, fn); err != nil {
				t.Fatal(err)
			}
		} else if err := d.InTenant(ctx, tenantID, db.TenantOpts{}, fn); err != nil {
			t.Fatal(err)
		}
		return n
	}
	other := newTenant(t, d)
	if n := count(c.TenantID); n != 1 {
		t.Fatalf("fts_match nel proprio tenant: %d, atteso 1", n)
	}
	if n := count(other.TenantID); n != 0 {
		t.Fatalf("fts_match da un altro tenant: %d, atteso 0", n)
	}
	if n := count(""); n != 0 {
		t.Fatalf("fts_match senza tenant: %d, atteso 0", n)
	}
}
