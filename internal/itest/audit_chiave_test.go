package itest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Registro di audit (migrazione 0085): la numerazione è per tenant, quindi due tenant possono
// scrivere lo stesso numero nello stesso istante. Prima la chiave primaria (at, seq) li faceva
// scontrare: due organizzazioni create insieme davano 500.
func TestAuditChiavePerTenant(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := time.Now().Format("150405.000000")

	// Deterministico: lo stesso (at, seq) in due tenant diversi si salva; nello stesso no.
	var a, b string
	for i, p := range []*string{&a, &b} {
		c, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: fmt.Sprintf("auk-%d-%s", i, stampSlug(stamp)), Name: "Audit", AdminEmail: fmt.Sprintf("a%d@auk-%s.test", i, stampSlug(stamp)), AdminPassword: testPassword})
		if err != nil {
			t.Fatal(err)
		}
		*p = c.TenantID
	}
	at := time.Date(2026, 11, 27, 10, 0, 0, 123456000, time.UTC)
	ins := func(tenantID string) error {
		_, err := d.Owner.Exec(ctx, `insert into audit_log (tenant_id, seq, at, action, entity_type, hash) values ($1, 999999, $2, 'test.audit', 'test', '\x00')`, tenantID, at)
		return err
	}
	if err := ins(a); err != nil {
		t.Fatal(err)
	}
	if err := ins(b); err != nil {
		t.Fatalf("stesso istante e stesso numero in un altro tenant: %v", err)
	}
	if err := ins(a); err == nil {
		t.Fatal("lo stesso numero nello stesso tenant deve restare unico")
	}
	if _, err := d.Owner.Exec(ctx, `delete from audit_log where seq = 999999 and action = 'test.audit'`); err != nil {
		t.Fatal(err)
	}

	// Concorrente: molti tenant scrivono insieme le stesse posizioni della catena; nessuno fallisce
	// e ogni catena resta integra.
	const tenants, events = 6, 60
	ids := make([]string, tenants)
	for i := range ids {
		c, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: fmt.Sprintf("auc-%d-%s", i, stampSlug(stamp)), Name: "Audit", AdminEmail: fmt.Sprintf("c%d@auc-%s.test", i, stampSlug(stamp)), AdminPassword: testPassword})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = c.TenantID
	}
	var wg sync.WaitGroup
	errs := make(chan error, tenants)
	start := make(chan struct{})
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			err := d.InTenant(ctx, id, db.TenantOpts{}, func(tx pgx.Tx) error {
				for range events {
					if err := audit.Append(ctx, tx, audit.Event{Action: "test.audit", EntityType: "test"}); err != nil {
						return err
					}
				}
				return nil
			})
			errs <- err
		}(id)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("scrittura concorrente nel registro: %v", err)
		}
	}
	for _, id := range ids {
		var broken *int64
		if err := d.Owner.QueryRow(ctx, `select audit_verify($1)`, id).Scan(&broken); err != nil {
			t.Fatal(err)
		}
		if broken != nil {
			t.Fatalf("catena del tenant %s rotta al numero %d", id, *broken)
		}
	}

	// Due organizzazioni create insieme, con il provisioning completo, come dall'interfaccia.
	var wg2 sync.WaitGroup
	cerrs := make(chan error, 2)
	for i := range 2 {
		wg2.Add(1)
		go func(i int) {
			defer wg2.Done()
			_, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: fmt.Sprintf("aup-%d-%s", i, stampSlug(stamp)), Name: "Insieme", AdminEmail: fmt.Sprintf("p%d@aup-%s.test", i, stampSlug(stamp)),
				AdminPassword: testPassword, Provision: api.Provisioners()})
			cerrs <- err
		}(i)
	}
	wg2.Wait()
	close(cerrs)
	for err := range cerrs {
		if err != nil {
			t.Fatalf("due organizzazioni create insieme: %v", err)
		}
	}
}

func stampSlug(s string) string {
	out := []byte{}
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			out = append(out, s[i])
		}
	}
	return string(out)
}
