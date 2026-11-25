package itest

import (
	"context"
	"io"
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
	"regia/internal/engine/notify"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/finance"
	"regia/internal/modules/grc"
	"regia/internal/modules/knowledge"
	"regia/internal/modules/portfolio"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/modules/risk"
	"regia/internal/modules/work"

	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/i18n"
	"regia/internal/platform/tenant"
)

// Lingua, passo 3 (replica/lingua.md): notifiche ed email nella lingua di chi le riceve, nomi dei
// fatti e delle azioni nella lingua di chi chiama, tenant nuovi in inglese, nomi dei tipi di
// sistema per lingua nei tenant esistenti.

// emailFor trova l'email spedita a un indirizzo (destinatario, oggetto, corpo).
func emailFor(f *fakeMail, to string) (subject, body string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.sent {
		parts := strings.SplitN(m, "\n", 3)
		if parts[0] == to {
			return parts[1], parts[2], true
		}
	}
	return "", "", false
}

func TestNotificationEmailInRecipientLanguage(t *testing.T) {
	e := newSGEnv(t, "linguab")
	ctx := context.Background()
	admin := login(t, e.srv.URL, "admin@"+e.slug+".test", testPassword)
	ita := e.member("ita", tenant.MemberInput{Roles: []string{"editor"}})
	eng := e.member("eng", tenant.MemberInput{Roles: []string{"editor"}})
	// Una persona sceglie l'inglese, l'altra non sceglie nulla (italiano, il tenant non ha un default).
	eng.do("PUT", "/api/v1/me/language", map[string]any{"locale": "en"}, http.StatusOK)

	notify.EnableEmail(true)
	defer notify.EnableEmail(false)
	// Lo stesso evento (sponsor di una richiesta) per due persone di lingua diversa, nello stesso tenant.
	for _, sp := range []*user{ita, eng} {
		admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Registro fornitori", "description": "Art. 28", "sponsor_id": sp.member}, http.StatusCreated)
	}

	mail := &fakeMail{}
	mailer := notify.NewMailer(testDB, mail, time.Minute, "https://regia.example", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := mailer.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	subIt, bodyIt, ok := emailFor(mail, "ita@"+e.slug+".test")
	if !ok {
		t.Fatal("nessuna email alla persona in italiano")
	}
	subEn, bodyEn, ok := emailFor(mail, "eng@"+e.slug+".test")
	if !ok {
		t.Fatal("nessuna email alla persona in inglese")
	}
	if !strings.Contains(subIt, "ti ha assegnato il ruolo sponsor") || !strings.Contains(bodyIt, "Novità su Regia") ||
		!strings.Contains(bodyIt, "Tutte le notifiche") || !strings.Contains(bodyIt, "Puoi scegliere quali email ricevere") {
		t.Fatalf("email in italiano: %q\n%s", subIt, bodyIt)
	}
	if !strings.Contains(subEn, "assigned you the sponsor role") || !strings.Contains(bodyEn, "What's new in Regia") ||
		!strings.Contains(bodyEn, "All notifications") || !strings.Contains(bodyEn, "You can choose which emails you get") {
		t.Fatalf("email in inglese: %q\n%s", subEn, bodyEn)
	}
	for _, it := range []string{"Novità", "ti ha assegnato", "Tutte le notifiche"} {
		if strings.Contains(bodyEn, it) || strings.Contains(subEn, it) {
			t.Fatalf("testo italiano nell'email inglese: %q", it)
		}
	}

	// Il default del tenant vale per chi non ha scelto una lingua: chi era in italiano passa
	// all'inglese, chi aveva scelto resta com'è.
	admin.do("PUT", "/api/v1/tenant/language", map[string]any{"locale": "en"}, http.StatusOK)
	var lang string
	err := testDB.InTenant(ctx, e.tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		lang, err = i18n.MemberLang(ctx, tx, ita.member)
		return err
	})
	if err != nil || lang != "en" {
		t.Fatalf("lingua di chi segue il tenant: %q %v", lang, err)
	}
}

// newLangEnv crea un tenant con tutti i moduli nella lingua indicata e un server di prova.
func newLangEnv(t *testing.T, lang string) (slug, tenantID string, admin *user, all []tenant.Provisioner) {
	t.Helper()
	d := needDB(t)
	ctx := context.Background()
	all = []tenant.Provisioner{demand.Provision, architecture.Provision, portfolio.Provision, finance.Provision,
		reviews.Provision, work.Provision, requirements.Provision, risk.Provision, grc.Provision, knowledge.Provision}
	slug = strings.ReplaceAll("lingua-"+lang+"-"+time.Now().Format("150405.000000"), ".", "")
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Lingua " + lang, Language: lang,
		AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: all})
	if err != nil {
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
	return slug, ten.TenantID, login(t, srv.URL, "admin@"+slug+".test", testPassword), all
}

func nameOfType(items []any, key string) string {
	for _, x := range items {
		if m := x.(map[string]any); m["key"] == key {
			return m["name"].(string)
		}
	}
	return ""
}

func TestProvisioningInEnglish(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug, tid, admin, all := newLangEnv(t, "en")

	// La lingua scelta alla creazione è la predefinita del tenant.
	me := admin.do("GET", "/api/v1/me", nil, http.StatusOK)
	wantLang(t, "tenant in inglese", me, "en", "tenant")

	// Tipi: nome inglese (e l'italiano per chi lo sceglie), moduli diversi.
	def := admin.do("GET", "/api/v1/type-names", nil, http.StatusOK)["items"].([]any)
	it := admin.do("GET", "/api/v1/type-names?lang=it", nil, http.StatusOK)["items"].([]any)
	for key, want := range map[string][2]string{
		"business_request": {"Request", "Richiesta"}, "impact": {"Impact", "Impatto"}, "application": {"Application", "Applicazione"},
		"risk": {"ICT risk", "Rischio ICT"}, "control": {"Control", "Controllo"}, "page": {"Document", "Documento"}, "review": {"Review", "Valutazione"},
	} {
		if g := nameOfType(def, key); g != want[0] {
			t.Errorf("tipo %s: %q, voglio %q", key, g, want[0])
		}
		if g := nameOfType(it, key); g != want[1] {
			t.Errorf("tipo %s in italiano: %q, voglio %q", key, g, want[1])
		}
	}

	one := func(q string, args ...any) string {
		var v string
		if err := d.Owner.QueryRow(ctx, q, append([]any{tid}, args...)...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return v
	}
	for q, want := range map[string]string{
		`select name from roles where tenant_id = $1 and key = 'viewer'`:                        "Reader",
		`select name from roles where tenant_id = $1 and key = 'process_admin'`:                 "Process administrator",
		`select name from groups where tenant_id = $1 and system_kind = 'all_members'`:          "All members",
		`select name from stakeholder_roles where tenant_id = $1 and key = 'sponsor'`:           "Sponsor",
		`select name from stakeholder_roles where tenant_id = $1 and key = 'technical_analyst'`: "Technical analyst",
		`select name from spaces where tenant_id = $1 and key = 'RIS'`:                          "ICT risks",
		`select outward_name from relation_types where tenant_id = $1 and key = 'uses'`:         "uses",
		`select name from process_definitions where tenant_id = $1 and key = 'story_standard'`:  "Story lifecycle",
		`select name from field_defs where tenant_id = $1 and key = 'priorita'`:                 "Priority",
	} {
		if got := one(q); got != want {
			t.Errorf("%s: %q, voglio %q", q, got, want)
		}
	}

	// Il provisioning ripetuto non duplica né riporta in italiano: i tipi restano gli stessi.
	var before, after int
	count := `select count(*) from element_types where tenant_id = $1`
	if err := d.Owner.QueryRow(ctx, count, tid).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := tenant.Provision(ctx, d, slug, all); err != nil {
		t.Fatalf("secondo provisioning: %v", err)
	}
	if err := d.Owner.QueryRow(ctx, count, tid).Scan(&after); err != nil || after != before {
		t.Fatalf("tipi dopo il secondo provisioning: %d, prima %d (%v)", after, before, err)
	}
	if got := one(`select name from element_types where tenant_id = $1 and key = 'business_request'`); got != "Request" {
		t.Fatalf("dopo il secondo provisioning il tipo è %q", got)
	}
}

func TestSystemTypesHaveEnglishLabels(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	// Un tenant italiano con tutti i moduli: ogni tipo di sistema ha labels.en (migrazione 0052 e
	// trigger per quelli creati dopo), e /type-names?lang=en li dà in inglese.
	_, tid, admin, _ := newLangEnv(t, "it")
	var missing int
	if err := d.Owner.QueryRow(ctx, `select count(*) from element_types where tenant_id = $1 and is_system and not labels ? 'en'`, tid).Scan(&missing); err != nil || missing != 0 {
		t.Fatalf("tipi di sistema senza nome inglese: %d (%v)", missing, err)
	}
	en := admin.do("GET", "/api/v1/type-names?lang=en", nil, http.StatusOK)["items"].([]any)
	it := admin.do("GET", "/api/v1/type-names", nil, http.StatusOK)["items"].([]any)
	if nameOfType(en, "business_request") != "Request" || nameOfType(en, "ict_provider") != "ICT provider" || nameOfType(en, "exception") != "Waiver" {
		t.Fatalf("nomi inglesi dei tipi: %q %q %q", nameOfType(en, "business_request"), nameOfType(en, "ict_provider"), nameOfType(en, "exception"))
	}
	if nameOfType(it, "business_request") != "Richiesta" {
		t.Fatalf("nome italiano: %q", nameOfType(it, "business_request"))
	}
	// Un nome inglese già scelto dal tenant non si sovrascrive: il riempimento tocca solo chi non ce l'ha.
	if _, err := d.Owner.Exec(ctx, `update element_types set labels = '{"en": {"name": "Demand"}}' where tenant_id = $1 and key = 'business_request'`, tid); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Owner.Exec(ctx, `update element_types set labels = labels || jsonb_build_object('en', system_type_label_en(key))
		where tenant_id = $1 and is_system and not labels ? 'en' and system_type_label_en(key) is not null`, tid); err != nil {
		t.Fatal(err)
	}
	en = admin.do("GET", "/api/v1/type-names?lang=en", nil, http.StatusOK)["items"].([]any)
	if nameOfType(en, "business_request") != "Demand" {
		t.Fatalf("nome inglese scelto dal tenant: %q", nameOfType(en, "business_request"))
	}
}

func TestFactAndActionNamesInCallerLanguage(t *testing.T) {
	_, _, admin, _ := newLangEnv(t, "it")
	factName := func(meta map[string]any, key string) string {
		for _, f := range meta["facts"].([]any) {
			if m := f.(map[string]any); m["key"] == key {
				return m["name"].(string)
			}
		}
		return ""
	}
	actionName := func(meta map[string]any, kind string) string {
		for _, a := range meta["actions"].([]any) {
			if m := a.(map[string]any); m["kind"] == kind {
				return m["name"].(string)
			}
		}
		return ""
	}
	path := "/api/v1/processes/demand_standard/meta"
	meta := admin.do("GET", path, nil, http.StatusOK)
	if factName(meta, "stato") != "Fase attuale" || actionName(meta, "assign_role") != "Assegna un ruolo" {
		t.Fatalf("meta in italiano: %q %q", factName(meta, "stato"), actionName(meta, "assign_role"))
	}
	// La stessa richiesta con la persona in inglese: nomi dei fatti e delle azioni in inglese.
	admin.do("PUT", "/api/v1/me/language", map[string]any{"locale": "en"}, http.StatusOK)
	meta = admin.do("GET", path, nil, http.StatusOK)
	if factName(meta, "stato") != "Current phase" || factName(meta, "tipo") != "Item type" || actionName(meta, "assign_role") != "Assign a role" {
		t.Fatalf("meta in inglese: %q %q %q", factName(meta, "stato"), factName(meta, "tipo"), actionName(meta, "assign_role"))
	}
	for _, f := range meta["facts"].([]any) {
		m := f.(map[string]any)
		if m["source"] == "ruolo" && !strings.HasPrefix(m["name"].(string), "Role ") {
			t.Fatalf("nome del fatto di un ruolo in inglese: %q", m["name"])
		}
	}
	// I controlli del gate di rilascio: la politica di partenza nella lingua di chi legge.
	pol := admin.do("GET", "/api/v1/devsecops/policy", nil, http.StatusOK)
	ctl := pol["definition"].(map[string]any)["controls"].([]any)[0].(map[string]any)
	if ctl["name"] != "Automated tests" {
		t.Fatalf("controllo della politica in inglese: %v", ctl["name"])
	}
	// «Sistema» nella storia diventa «System» (chi non ha un autore).
	admin.do("PUT", "/api/v1/me/language", map[string]any{"locale": "it"}, http.StatusOK)
	pol = admin.do("GET", "/api/v1/devsecops/policy", nil, http.StatusOK)
	if n := pol["definition"].(map[string]any)["controls"].([]any)[0].(map[string]any)["name"]; n != "Test automatici" {
		t.Fatalf("controllo della politica in italiano: %v", n)
	}
}
