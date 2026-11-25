package itest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Le operazioni di amministrazione girano col ruolo proprietario (superuser): devono
// comunque restare confinate nel proprio tenant. Regressione del 2026-11-24: con più
// tenant, l'aggiunta di un membro prendeva i gruppi di tutti e il provisioning del
// secondo tenant veniva saltato perché "vedeva" il tipo del primo.
func TestAdminOperationsStayInTenant(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	var ids []string
	var slugs []string
	for i := 0; i < 2; i++ {
		c := newTenant(t, d)
		var slug string
		if err := d.Owner.QueryRow(ctx, "select slug from tenants where id = $1", c.TenantID).Scan(&slug); err != nil {
			t.Fatal(err)
		}
		if err := tenant.Provision(ctx, d, slug, []tenant.Provisioner{demand.Provision, architecture.Provision}); err != nil {
			t.Fatal(err)
		}
		ids, slugs = append(ids, c.TenantID), append(slugs, slug)
	}
	if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{
		TenantSlug: slugs[1], Email: "pmo-" + slugs[1] + "@example.test", Password: testPassword,
		Roles: []string{"editor"}, Groups: []string{"PMO"},
	}); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		err := d.InTenant(ctx, id, db.TenantOpts{}, func(tx pgx.Tx) error {
			var types, spaces, groupsOfOthers int
			if err := tx.QueryRow(ctx, `select
				(select count(*) from element_types where key = 'business_request'),
				(select count(*) from spaces where key = 'DEM'),
				(select count(*) from group_members gm join groups g on g.tenant_id = gm.tenant_id and g.id = gm.group_id where g.tenant_id <> $1)`, id).
				Scan(&types, &spaces, &groupsOfOthers); err != nil {
				return err
			}
			if types != 1 || spaces != 1 || groupsOfOthers != 0 {
				t.Errorf("tenant %d: tipi %d, spazi %d, gruppi di altri tenant %d", i, types, spaces, groupsOfOthers)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Nessuna riga di group_members punta a un gruppo di un altro tenant (verifica globale).
	var cross int
	if err := d.Owner.QueryRow(ctx, `select count(*) from group_members gm join groups g on g.id = gm.group_id where g.tenant_id <> gm.tenant_id`).Scan(&cross); err != nil {
		t.Fatal(err)
	}
	if cross != 0 {
		t.Fatalf("%d appartenenze a gruppi di altri tenant", cross)
	}
}

// Il provisioning aggiunge ai tenant esistenti i ruoli di sistema nati dopo la loro creazione.
func TestProvisionAddsMissingSystemRoles(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	ten := newTenant(t, d)
	if _, err := d.Owner.Exec(ctx, "delete from roles where tenant_id = $1 and key = 'architect'", ten.TenantID); err != nil {
		t.Fatal(err)
	}
	var slug string
	if err := d.Owner.QueryRow(ctx, "select slug from tenants where id = $1", ten.TenantID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	if err := tenant.Provision(ctx, d, slug, nil); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.Owner.QueryRow(ctx, "select count(*) from roles where tenant_id = $1 and key = 'architect'", ten.TenantID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("ruolo architetto: %d", n)
	}
}
