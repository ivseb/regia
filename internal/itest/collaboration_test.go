package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/engine/notify"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// upload invia un file in multipart; restituisce il JSON della risposta.
func (u *user) upload(path, name string, content []byte, commentID string, wantStatus int) map[string]any {
	u.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if commentID != "" {
		_ = mw.WriteField("comment_id", commentID)
	}
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(content)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", u.base+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := u.c.Do(req)
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		u.t.Fatalf("upload %s: stato %d, atteso %d: %s", name, resp.StatusCode, wantStatus, raw)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

// get restituisce stato, intestazioni e corpo grezzo.
func (u *user) get(path string) (int, http.Header, []byte) {
	u.t.Helper()
	resp, err := u.c.Get(u.base + path)
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

func (u *user) notes() []map[string]any {
	u.t.Helper()
	var out []map[string]any
	for _, it := range u.do("GET", "/api/v1/notifications", nil, http.StatusOK)["items"].([]any) {
		out = append(out, it.(map[string]any))
	}
	return out
}

func (u *user) unread() int {
	u.t.Helper()
	return int(u.do("GET", "/api/v1/notifications/count", nil, http.StatusOK)["unread"].(float64))
}

// kinds conta le notifiche per tipo (per un elemento, se key non è vuota).
func kinds(items []map[string]any, key string) map[string]int {
	out := map[string]int{}
	for _, it := range items {
		if key == "" || it["element_key"] == key {
			out[it["kind"].(string)]++
		}
	}
	return out
}

type fakeMail struct {
	mu   sync.Mutex
	sent []string // destinatario + oggetto + corpo
	fail bool
}

func (f *fakeMail) Send(_ context.Context, to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("server SMTP non raggiungibile")
	}
	f.sent = append(f.sent, to+"\n"+subject+"\n"+body)
	return nil
}

func pngBytes(t *testing.T, w, h int) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Commenti, menzioni, allegati, osservatori e notifiche (piano, passo 3).
func TestCollaboration(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("col-"+time.Now().Format("150405.000"), ".", "")
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Collaborazione", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
	})
	if err != nil {
		t.Fatal(err)
	}
	add := func(local, name string, in tenant.MemberInput) {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, local+"@"+slug+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	add("req", "Luca Neri", tenant.MemberInput{MemberType: "requester", Roles: []string{"requester"}})
	add("dm", "Sara Villa", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	add("spo", "Marta Ferri", tenant.MemberInput{Roles: []string{"viewer"}})
	add("pmo", "Davide Costa", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"PMO"}})
	add("cur", "Carlo Curioso", tenant.MemberInput{MemberType: "requester", Roles: []string{"requester"}})
	add("lead", "Paolo Riva", tenant.MemberInput{Roles: []string{"viewer"}})
	add("arch", "Elena Conti", tenant.MemberInput{Roles: []string{"architect"}})

	// Secondo tenant, per l'isolamento.
	slugB := slug + "b"
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slugB, Name: "Altro", AdminEmail: "admin@" + slugB + ".test",
		AdminPassword: testPassword, Provision: []tenant.Provisioner{demand.Provision}}); err != nil {
		t.Fatal(err)
	}

	files := t.TempDir()
	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
		FilesDir: files, MaxUploadBytes: 1 << 20,
	}).Handler())
	defer srv.Close()

	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	req := login(t, srv.URL, "req@"+slug+".test", testPassword)
	man := login(t, srv.URL, "dm@"+slug+".test", testPassword)
	spo := login(t, srv.URL, "spo@"+slug+".test", testPassword)
	pmo := login(t, srv.URL, "pmo@"+slug+".test", testPassword)
	cur := login(t, srv.URL, "cur@"+slug+".test", testPassword)
	lead := login(t, srv.URL, "lead@"+slug+".test", testPassword)
	archi := login(t, srv.URL, "arch@"+slug+".test", testPassword)
	other := login(t, srv.URL, "admin@"+slugB+".test", testPassword)

	// Le email partono solo se il server SMTP è configurato: qui con un finto mittente.
	notify.EnableEmail(true)
	defer notify.EnableEmail(false)

	// Il richiedente apre la richiesta con lo sponsor: lo sponsor riceve "ruolo assegnato",
	// il richiedente nulla (non si notifica sé stesso).
	created := req.do("POST", "/api/v1/demand/requests", map[string]any{
		"title": "Registro dei fornitori ICT", "description": "Art. 28 DORA", "sponsor_id": spo.member,
	}, http.StatusCreated)
	key, id := created["key"].(string), created["id"].(string)
	if k := kinds(spo.notes(), key); k["role"] != 1 {
		t.Fatalf("notifiche dello sponsor dopo la creazione: %v", k)
	}
	if n := req.unread(); n != 0 {
		t.Fatalf("il richiedente ha %d notifiche per le proprie azioni", n)
	}

	// Commento con menzioni: Sara (editor) riceve la menzione; Carlo non vede la richiesta e
	// non riceve nulla; Marta (stakeholder) riceve "nuovo commento".
	c1 := req.do("POST", "/api/v1/elements/"+key+"/comments", map[string]any{
		"body": "Ciao @Sara Villa, puoi guardare **l'allegato**? Anche @carlo curioso.",
	}, http.StatusCreated)
	c1id := c1["id"].(string)
	if ms := c1["mentions"].([]any); len(ms) != 2 {
		t.Fatalf("menzioni riconosciute: %v", ms)
	}
	if k := kinds(man.notes(), key); k["mention"] != 1 || k["comment"] != 0 {
		t.Fatalf("notifiche di Sara: %v", k)
	}
	if n := len(cur.notes()); n != 0 {
		t.Fatalf("chi non vede la richiesta ha ricevuto %d notifiche", n)
	}
	if k := kinds(spo.notes(), key); k["comment"] != 1 {
		t.Fatalf("lo sponsor non ha ricevuto il nuovo commento: %v", k)
	}
	note := man.notes()[0]
	if note["actor_name"] != "Luca Neri" || note["element_key"] != key || !strings.Contains(note["payload"].(map[string]any)["excerpt"].(string), "l'allegato") {
		t.Fatalf("notifica della menzione: %v", note)
	}

	// Permessi: chi non legge l'elemento non ne legge né scrive i commenti; chi legge ma
	// non ha comment.write non scrive.
	cur.do("GET", "/api/v1/elements/"+key+"/comments", nil, http.StatusNotFound)
	cur.do("POST", "/api/v1/elements/"+key+"/comments", map[string]any{"body": "ciao"}, http.StatusNotFound)
	spo.do("POST", "/api/v1/elements/"+key+"/comments", map[string]any{"body": "ciao"}, http.StatusForbidden)
	if n := len(spo.do("GET", "/api/v1/elements/"+key+"/comments", nil, http.StatusOK)["items"].([]any)); n != 1 {
		t.Fatalf("lo sponsor vede %d commenti", n)
	}
	req.do("POST", "/api/v1/elements/"+key+"/comments", map[string]any{"body": "   "}, http.StatusUnprocessableEntity)

	// Modifica: solo l'autore; i nuovi menzionati ricevono la notifica, i vecchi no.
	man.do("PATCH", "/api/v1/comments/"+c1id, map[string]any{"body": "modifica abusiva"}, http.StatusForbidden)
	man.do("DELETE", "/api/v1/comments/"+c1id, nil, http.StatusForbidden)
	ed := req.do("PATCH", "/api/v1/comments/"+c1id, map[string]any{
		"body": "Ciao @Sara Villa e @Davide Costa, guardate l'allegato.",
	}, http.StatusOK)
	if ed["edited_at"] == nil {
		t.Fatalf("manca la traccia della modifica: %v", ed)
	}
	if k := kinds(man.notes(), key); k["mention"] != 1 {
		t.Fatalf("Sara ha ricevuto di nuovo la menzione: %v", k)
	}
	if k := kinds(pmo.notes(), key); k["mention"] != 1 {
		t.Fatalf("Davide non ha ricevuto la menzione aggiunta: %v", k)
	}

	// Segui e non seguire. Paolo (lettore, nessun ruolo) segue la richiesta e riceve il cambio
	// di fase; lo sponsor la segue in automatico (stakeholder) finché non smette.
	if st := lead.do("GET", "/api/v1/elements/"+key+"/watch", nil, http.StatusOK); st["following"] != false {
		t.Fatalf("Paolo segue già la richiesta: %v", st)
	}
	if st := lead.do("PUT", "/api/v1/elements/"+key+"/watch", nil, http.StatusOK); st["following"] != true || st["auto"] != false {
		t.Fatalf("segui: %v", st)
	}
	if st := spo.do("GET", "/api/v1/elements/"+key+"/watch", nil, http.StatusOK); st["following"] != true || st["auto"] != true {
		t.Fatalf("lo sponsor non segue in automatico: %v", st)
	}
	cur.do("PUT", "/api/v1/elements/"+key+"/watch", nil, http.StatusNotFound)
	req.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	if k := kinds(lead.notes(), key); k["phase"] != 1 {
		t.Fatalf("Paolo non ha ricevuto il cambio di fase: %v", k)
	}
	if k := kinds(spo.notes(), key); k["phase"] != 1 {
		t.Fatalf("lo sponsor non ha ricevuto il cambio di fase: %v", k)
	}
	if st := lead.do("DELETE", "/api/v1/elements/"+key+"/watch", nil, http.StatusOK); st["following"] != false {
		t.Fatalf("Paolo segue ancora: %v", st)
	}
	if st := spo.do("DELETE", "/api/v1/elements/"+key+"/watch", nil, http.StatusOK); st["following"] != false || st["auto"] != true {
		t.Fatalf("lo sponsor segue ancora: %v", st)
	}
	man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Triage"}, http.StatusOK)
	if k := kinds(lead.notes(), key); k["phase"] != 1 {
		t.Fatalf("Paolo riceve ancora i cambi di fase: %v", k)
	}
	if k := kinds(spo.notes(), key); k["phase"] != 1 {
		t.Fatalf("lo sponsor riceve ancora i cambi di fase: %v", k)
	}
	if k := kinds(req.notes(), key); k["phase"] != 1 {
		t.Fatalf("il richiedente (autore) non ha ricevuto il triage: %v", k)
	}

	// Preferenze: Sara silenzia i commenti; una menzione la riceve ancora.
	prefs := man.do("PUT", "/api/v1/me/notification-prefs", map[string]any{"kinds": []map[string]any{
		{"kind": "comment", "in_app": false, "email": false}}}, http.StatusOK)
	if prefs["email_available"] != true {
		t.Fatalf("preferenze: %v", prefs)
	}
	man.do("PUT", "/api/v1/me/notification-prefs", map[string]any{"kinds": []map[string]any{{"kind": "boh", "in_app": false}}}, http.StatusUnprocessableEntity)
	before := kinds(man.notes(), key)
	req.do("POST", "/api/v1/elements/"+key+"/comments", map[string]any{"body": "Aggiornamento senza menzioni"}, http.StatusCreated)
	if k := kinds(man.notes(), key); k["comment"] != before["comment"] {
		t.Fatalf("Sara riceve i commenti silenziati: %v", k)
	}

	// Elenco, conteggio, segna come letta, segna tutte.
	n0 := man.unread()
	if n0 == 0 {
		t.Fatal("Sara non ha notifiche da leggere")
	}
	first := man.notes()[0]["id"].(string)
	pmo.do("POST", "/api/v1/notifications/"+first+"/read", nil, http.StatusNotFound)
	man.do("POST", "/api/v1/notifications/"+first+"/read", nil, http.StatusNoContent)
	if n := man.unread(); n != n0-1 {
		t.Fatalf("non lette dopo la lettura: %d, attese %d", n, n0-1)
	}
	if n := len(man.do("GET", "/api/v1/notifications?unread=true", nil, http.StatusOK)["items"].([]any)); n != n0-1 {
		t.Fatalf("elenco delle non lette: %d", n)
	}
	page := man.do("GET", "/api/v1/notifications?limit=1", nil, http.StatusOK)
	if page["next"] == nil {
		t.Fatalf("manca il cursore: %v", page)
	}
	if p2 := man.do("GET", "/api/v1/notifications?limit=1&cursor="+urlq(page["next"].(string)), nil, http.StatusOK)["items"].([]any); len(p2) != 1 ||
		p2[0].(map[string]any)["id"] == page["items"].([]any)[0].(map[string]any)["id"] {
		t.Fatalf("seconda pagina: %v", p2)
	}
	man.do("POST", "/api/v1/notifications/read-all", nil, http.StatusOK)
	if n := man.unread(); n != 0 {
		t.Fatalf("non lette dopo 'segna tutte': %d", n)
	}

	// Allegati: tipo dal contenuto, dimensioni delle immagini, deduplica, limiti.
	img := pngBytes(t, 40, 30)
	a1 := req.upload("/api/v1/elements/"+key+"/attachments", "schema.png", img, "", http.StatusCreated)
	if a1["mime_type"] != "image/png" || a1["width"] != float64(40) || a1["preview"] != true {
		t.Fatalf("allegato immagine: %v", a1)
	}
	a2 := req.upload("/api/v1/elements/"+key+"/attachments", "copia.png", img, c1id, http.StatusCreated)
	if a2["sha256"] != a1["sha256"] {
		t.Fatalf("stesso contenuto, sha diversi: %v %v", a1["sha256"], a2["sha256"])
	}
	sha := a1["sha256"].(string)
	if _, err := os.Stat(filepath.Join(files, sha[0:2], sha[2:4], sha)); err != nil {
		t.Fatalf("file su disco: %v", err)
	}
	var blobs int
	_ = filepath.WalkDir(files, func(p string, de os.DirEntry, err error) error {
		if err == nil && !de.IsDir() && !strings.Contains(p, string(filepath.Separator)+"tmp"+string(filepath.Separator)) {
			blobs++
		}
		return nil
	})
	if blobs != 1 {
		t.Fatalf("deduplica: %d file su disco, atteso 1", blobs)
	}
	evil := req.upload("/api/v1/elements/"+key+"/attachments", "foto.png", []byte("<html><script>alert(1)</script></html>"), "", http.StatusCreated)
	if evil["mime_type"] != "text/html" || evil["preview"] != false {
		t.Fatalf("il tipo non viene dal contenuto: %v", evil)
	}
	req.upload("/api/v1/elements/"+key+"/attachments", "grande.bin", bytes.Repeat([]byte{1}, 1<<20+1), "", http.StatusRequestEntityTooLarge)
	req.upload("/api/v1/elements/"+key+"/attachments", "commento.txt", []byte("x"), c1id[:8], http.StatusUnprocessableEntity)
	man.upload("/api/v1/elements/"+key+"/attachments", "altrui.txt", []byte("x"), c1id, http.StatusUnprocessableEntity)
	spo.upload("/api/v1/elements/"+key+"/attachments", "sponsor.txt", []byte("x"), "", http.StatusForbidden)
	cur.upload("/api/v1/elements/"+key+"/attachments", "curioso.txt", []byte("x"), "", http.StatusNotFound)

	// Download sicuro.
	st, h, body := spo.get("/api/v1/attachments/" + a1["id"].(string) + "/content")
	if st != 200 || !bytes.Equal(body, img) || h.Get("Content-Type") != "image/png" || !strings.HasPrefix(h.Get("Content-Disposition"), "inline") ||
		h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download immagine: %d %v", st, h)
	}
	if _, h, _ := spo.get("/api/v1/attachments/" + a1["id"].(string) + "/content?download=1"); !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download forzato: %v", h)
	}
	st, h, _ = spo.get("/api/v1/attachments/" + evil["id"].(string) + "/content")
	if st != 200 || h.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") ||
		!strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("download HTML: %d %v", st, h)
	}
	if st, _, _ := cur.get("/api/v1/attachments/" + a1["id"].(string) + "/content"); st != http.StatusNotFound {
		t.Fatalf("chi non vede la richiesta scarica l'allegato: %d", st)
	}
	cur.do("GET", "/api/v1/elements/"+key+"/attachments", nil, http.StatusNotFound)

	// L'allegato del commento compare sotto il commento; eliminazione logica con audit.
	comments := admin.do("GET", "/api/v1/elements/"+key+"/comments", nil, http.StatusOK)["items"].([]any)
	if atts := comments[0].(map[string]any)["attachments"].([]any); len(atts) != 1 {
		t.Fatalf("allegati del commento: %v", atts)
	}
	if n := len(admin.do("GET", "/api/v1/elements/"+key+"/attachments", nil, http.StatusOK)["items"].([]any)); n != 3 {
		t.Fatalf("allegati dell'elemento: %d, attesi 3", n)
	}
	man.do("DELETE", "/api/v1/attachments/"+evil["id"].(string), nil, http.StatusForbidden)
	req.do("DELETE", "/api/v1/attachments/"+evil["id"].(string), nil, http.StatusNoContent)
	req.do("DELETE", "/api/v1/attachments/"+evil["id"].(string), nil, http.StatusNotFound)
	if st, _, _ := admin.get("/api/v1/attachments/" + evil["id"].(string) + "/content"); st != http.StatusNotFound {
		t.Fatalf("allegato eliminato ancora scaricabile: %d", st)
	}
	if n := len(admin.do("GET", "/api/v1/elements/"+key+"/attachments", nil, http.StatusOK)["items"].([]any)); n != 2 {
		t.Fatalf("allegati dopo l'eliminazione: %d", n)
	}

	// Isolamento tra tenant: niente commenti, allegati, osservatori o notifiche altrui.
	other.do("GET", "/api/v1/elements/"+id+"/comments", nil, http.StatusNotFound)
	other.do("POST", "/api/v1/elements/"+id+"/comments", map[string]any{"body": "intruso"}, http.StatusNotFound)
	other.do("PATCH", "/api/v1/comments/"+c1id, map[string]any{"body": "intruso"}, http.StatusNotFound)
	other.do("DELETE", "/api/v1/comments/"+c1id, nil, http.StatusNotFound)
	other.do("PUT", "/api/v1/elements/"+id+"/watch", nil, http.StatusNotFound)
	other.upload("/api/v1/elements/"+id+"/attachments", "x.txt", []byte("x"), "", http.StatusNotFound)
	if st, _, _ := other.get("/api/v1/attachments/" + a1["id"].(string) + "/content"); st != http.StatusNotFound {
		t.Fatalf("download da un altro tenant: %d", st)
	}
	other.do("POST", "/api/v1/notifications/"+first+"/read", nil, http.StatusNotFound)
	if n := len(other.notes()); n != 0 {
		t.Fatalf("l'altro tenant vede %d notifiche", n)
	}

	// Approvazioni: il passo attivo notifica lo sponsor, poi il gruppo PMO.
	for _, to := range []string{"Valutazione"} {
		man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": to}, http.StatusOK)
	}
	man.do("PATCH", "/api/v1/demand/requests/"+id, map[string]any{"estimate_low_cents": 8_000_000, "estimate_high_cents": 12_000_000}, http.StatusNoContent)
	man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Prioritizzazione"}, http.StatusOK)
	man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Approvazione"}, http.StatusOK)
	if k := kinds(spo.notes(), key); k["approval"] != 1 {
		t.Fatalf("lo sponsor non è stato avvisato dell'approvazione: %v", k)
	}
	if k := kinds(pmo.notes(), key); k["approval"] != 0 {
		t.Fatalf("il PMO avvisato prima del suo turno: %v", k)
	}
	step := spo.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any)[0].(map[string]any)["step_id"].(string)
	spo.do("POST", "/api/v1/approvals/steps/"+step+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	if k := kinds(pmo.notes(), key); k["approval"] != 1 {
		t.Fatalf("il PMO non è stato avvisato del suo turno: %v", k)
	}

	// Nomina a referente del team su un impatto.
	team := archi.do("POST", "/api/v1/teams", map[string]any{"name": "Web", "lead_id": lead.member}, http.StatusCreated)
	app := archi.do("POST", "/api/v1/architecture/applications", map[string]any{"name": "Portale fornitori", "code": "PF", "team_id": team["id"]}, http.StatusCreated)
	imp := man.do("POST", "/api/v1/demand/requests/"+key+"/impacts", map[string]any{"application_id": app["id"]}, http.StatusCreated)
	if k := kinds(lead.notes(), imp["key"].(string)); k["referent"] != 1 {
		t.Fatalf("il referente del team non è stato avvisato: %v", k)
	}

	// Nessuna auto-notifica in tutto il flusso.
	var self int
	if err := d.Owner.QueryRow(ctx, "select count(*) from notifications where tenant_id = $1 and actor_id = member_id", ten.TenantID).Scan(&self); err != nil {
		t.Fatal(err)
	}
	if self != 0 {
		t.Fatalf("%d notifiche a chi ha compiuto l'azione", self)
	}

	// Storia e audit.
	hist := admin.do("GET", "/api/v1/elements/"+id+"/activity", nil, http.StatusOK)["items"].([]any)
	hk := map[string]int{}
	for _, a := range hist {
		m := a.(map[string]any)
		fk, _ := m["field_key"].(string)
		hk[m["kind"].(string)+"."+fk]++
	}
	if hk["comment.added"] != 2 || hk["comment.edited"] != 1 || hk["attachment.added"] != 3 || hk["attachment.removed"] != 1 {
		t.Fatalf("storia della collaborazione: %v", hk)
	}
	var audits int
	if err := d.Owner.QueryRow(ctx, `select count(*) from audit_log where tenant_id = $1 and action in
		('comment.create', 'comment.update', 'attachment.create', 'attachment.remove')`, ten.TenantID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 2+1+3+1 {
		t.Fatalf("voci di audit della collaborazione: %d", audits)
	}

	// Email: un riepilogo per persona; un errore SMTP riprogramma l'invio.
	mail := &fakeMail{fail: true}
	mailer := notify.NewMailer(d, mail, time.Minute, "https://regia.example", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := mailer.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var retry int
	if err := d.Owner.QueryRow(ctx, `select count(*) from outbox_events where tenant_id = $1 and dispatched_at is null
		and attempts = 1 and last_error is not null and next_attempt_at > now()`, ten.TenantID).Scan(&retry); err != nil {
		t.Fatal(err)
	}
	if retry == 0 {
		t.Fatal("dopo l'errore SMTP nessun evento da riprovare")
	}
	if _, err := d.Owner.Exec(ctx, `update outbox_events set next_attempt_at = now() where tenant_id = $1`, ten.TenantID); err != nil {
		t.Fatal(err)
	}
	mail.fail = false
	sent, err := mailer.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pending, emailed int
	if err := d.Owner.QueryRow(ctx, `select count(*) filter (where dispatched_at is null), 0 from outbox_events where tenant_id = $1`, ten.TenantID).Scan(&pending, &emailed); err != nil {
		t.Fatal(err)
	}
	if err := d.Owner.QueryRow(ctx, `select count(*) from notifications where tenant_id = $1 and emailed_at is not null`, ten.TenantID).Scan(&emailed); err != nil {
		t.Fatal(err)
	}
	recipients := map[string]bool{}
	for _, m := range mail.sent {
		recipients[strings.SplitN(m, "\n", 2)[0]] = true
	}
	if pending != 0 || sent == 0 || sent != len(recipients) || emailed == 0 {
		t.Fatalf("email: %d inviate a %d persone, %d in attesa, %d notifiche inviate", sent, len(recipients), pending, emailed)
	}
	// Le notifiche già lette in app (tutte quelle di Sara) non partono per email.
	if recipients["dm@"+slug+".test"] {
		t.Fatal("email a chi aveva già letto tutto in app")
	}
	var pmoMail string
	for _, m := range mail.sent {
		if strings.HasPrefix(m, "pmo@"+slug+".test") {
			pmoMail = m
		}
	}
	if !strings.Contains(pmoMail, "Luca Neri ti ha menzionato in "+key) || !strings.Contains(pmoMail, "https://regia.example/richieste?sel="+key) {
		t.Fatalf("email del PMO: %q", pmoMail)
	}
	if msg := string(notify.Message("Regia <noreply@example.com>", "a@b.it", "Oggetto\r\nBcc: x@y.z", "testo")); strings.Contains(msg, "\r\nBcc:") {
		t.Fatalf("iniezione di intestazioni: %q", msg)
	}

	// La catena di audit resta integra.
	var broken *int64
	if err := d.Owner.QueryRow(ctx, "select audit_verify($1)", ten.TenantID).Scan(&broken); err != nil {
		t.Fatal(err)
	}
	if broken != nil {
		t.Fatalf("catena di audit rotta al seq %d", *broken)
	}
}
