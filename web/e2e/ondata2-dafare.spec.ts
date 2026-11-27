// Ondata 2, proposta 1 «Da fare»: decisione in linea con motivazione, organo senza decisione in linea,
// impatto accettato dal referente del team, campanello che conta solo ciò che chiede un'azione, avvisi
// raggruppati per elemento con «Silenzia», stato vuoto, il telefono a 375 px e una vista in inglese.
import { test, expect, type APIRequestContext } from "@playwright/test";
import { advance, apiAs, call, expectNoHorizontalScroll, login, newRequest, scegli, uniq } from "./fixtures";

/** Richiesta portata alla fase di approvazione: lo sponsor (sotto 50 k€ basta lui) ha una decisione in attesa. */
async function requestInApproval(playwright: Parameters<typeof apiAs>[0], baseURL: string, label: string, amountK = 30) {
  const { req } = await apiAs(playwright, baseURL, "richiedente");
  const title = uniq(label);
  const { id, key } = await newRequest(req, title);
  await req.dispose();
  const dm = await apiAs(playwright, baseURL, "dm");
  await advance(dm.req, id, "Triage", "Valutazione");
  await call(dm.req, "PATCH", `/api/v1/demand/requests/${id}`, { estimate_low_cents: amountK * 100_000 - 1_000_000, estimate_high_cents: amountK * 100_000 });
  await advance(dm.req, id, "Prioritizzazione");
  const el = await call(dm.req, "GET", `/api/v1/elements/${id}`);
  await call(dm.req, "PATCH", `/api/v1/elements/${id}`, { version: el.element.version, fields: { data_golive: "2027-05-31" } });
  await advance(dm.req, id, "Approvazione");
  await dm.req.dispose();
  return { id, key, title };
}

async function total(req: APIRequestContext): Promise<number> {
  return (await call(req, "GET", "/api/v1/todo/count")).total;
}

test("decisione in linea: motivazione e Invio, con il contesto della richiesta", async ({ page, playwright, baseURL }) => {
  const { key, title } = await requestInApproval(playwright, baseURL!, "Archivio digitale delle delibere");
  await login(page, "sponsor");
  await page.goto("/da-fare");
  await expect(page.getByRole("heading", { name: "Da fare", level: 1 })).toBeVisible();
  const card = page.getByRole("listitem", { name: key, exact: true });
  await expect(card).toBeVisible();
  await expect(card.getByRole("link", { name: title })).toBeVisible();
  // Il contesto sta nella riga: importo e posizione in graduatoria.
  await expect(card.getByText("Importo 30 k€")).toBeVisible();
  await expect(card.getByText(/Posizione \d+ di \d+/)).toBeVisible();
  // «Respingi» chiede una motivazione: senza, non si invia.
  await card.getByRole("button", { name: "Respingi", exact: true }).click();
  const motivo = card.getByRole("textbox", { name: "Motivazione" });
  await expect(motivo).toBeFocused();
  await expect(card.getByRole("button", { name: "Invia", exact: true })).toBeDisabled();
  await motivo.fill("Il budget è già impegnato altrove.");
  await motivo.press("Enter");
  // La riga sparisce e la decisione è annunciata.
  await expect(card).toHaveCount(0);
  await expect(page.getByText(`${key}: decisione registrata, respinta.`)).toBeVisible();
  const d = await call(page.request, "GET", `/api/v1/elements/${key}`);
  expect(d.element.state).toBe("Respinta");
});

test("decisione in linea: approva con una nota facoltativa", async ({ page, playwright, baseURL }) => {
  const { key } = await requestInApproval(playwright, baseURL!, "Registro dei trattamenti fornitori");
  await login(page, "sponsor");
  await page.goto("/da-fare");
  const card = page.getByRole("listitem", { name: key, exact: true });
  await card.getByRole("button", { name: "Approva", exact: true }).click();
  await card.getByRole("button", { name: "Invia", exact: true }).click();
  await expect(card).toHaveCount(0);
  const d = await call(page.request, "GET", `/api/v1/elements/${key}`);
  expect(d.element.state).toBe("Pianificazione");
});

test("organo senza decisione in linea: «Apri il pacchetto» al posto dei pulsanti", async ({ page, playwright, baseURL }) => {
  const admin = await apiAs(playwright, baseURL!, "admin");
  const bodies = await call(admin.req, "GET", "/api/v1/governance/bodies");
  const body = bodies.items.find((b: { kind: string }) => b.kind === "investment_committee");
  const patch = (inline: boolean) =>
    call(admin.req, "PATCH", `/api/v1/governance/bodies/${body.id}`, { name: body.name, kind: body.kind, chair_id: body.chair_id, inline_decision: inline });
  // Una richiesta da 300 k€ arriva al comitato dopo sponsor e PMO.
  const { id, key } = await requestInApproval(playwright, baseURL!, "Migrazione del data warehouse", 300);
  const pmo = await apiAs(playwright, baseURL!, "pmo");
  const sponsor = await apiAs(playwright, baseURL!, "sponsor");
  const steps = async () => (await call(admin.req, "GET", `/api/v1/elements/${id}`)).approvals[0].steps as { id: string; decision: string }[];
  await call(sponsor.req, "POST", `/api/v1/approvals/steps/${(await steps())[0].id}/decision`, { decision: "approved" });
  await call(pmo.req, "POST", `/api/v1/approvals/steps/${(await steps())[1].id}/decision`, { decision: "approved" });
  await sponsor.req.dispose();
  await pmo.req.dispose();
  await patch(false);
  try {
    await login(page, "admin");
    await page.goto("/da-fare");
    const card = page.getByRole("listitem", { name: key, exact: true });
    await expect(card).toBeVisible();
    await expect(card.getByText("Questo organo vuole che si apra il pacchetto prima di decidere.")).toBeVisible();
    await expect(card.getByRole("button", { name: "Approva", exact: true })).toHaveCount(0);
    await card.getByRole("link", { name: "Apri il pacchetto" }).click();
    await expect(page).toHaveURL(new RegExp(`/e/${key}\\?scheda=approvazioni`));
    // Con l'impostazione di nuovo «in linea» i pulsanti tornano.
    await patch(true);
    await page.goto("/da-fare");
    await expect(page.getByRole("listitem", { name: key, exact: true }).getByRole("button", { name: "Approva", exact: true })).toBeVisible();
  } finally {
    await patch(true);
    await admin.req.dispose();
  }
});

test("impatti del mio team: il referente accetta il perimetro dalla pagina", async ({ page, playwright, baseURL }) => {
  const dm = await apiAs(playwright, baseURL!, "dm");
  const { id: reqId, key: reqKey } = await (async () => {
    const r = await apiAs(playwright, baseURL!, "richiedente");
    const out = await newRequest(r.req, uniq("Nuova area clienti"));
    await r.req.dispose();
    return out;
  })();
  // Un'applicazione del team Web (referente: lead@).
  const apps = await call(dm.req, "GET", "/api/v1/architecture/applications?q=");
  const app = apps.items.find((a: { team: string | null }) => a.team === "Web");
  expect(app, "applicazione del team Web").toBeTruthy();
  await advance(dm.req, reqId, "Triage");
  const impact = await call(dm.req, "POST", `/api/v1/demand/requests/${reqKey}/impacts`, { application_id: app.id, change_kind: "change" });
  await call(dm.req, "POST", `/api/v1/elements/${impact.id}/transitions`, { to: "In analisi" });
  await call(dm.req, "PATCH", `/api/v1/impacts/${impact.id}`, { estimate_effort_days: 4 });
  await call(dm.req, "POST", `/api/v1/elements/${impact.id}/transitions`, { to: "Stimato" });
  await dm.req.dispose();

  await login(page, "lead");
  await page.goto("/da-fare");
  const card = page.getByRole("listitem", { name: impact.key, exact: true });
  await expect(card).toBeVisible();
  await expect(card.getByText("Stima: 4 giorni")).toBeVisible();
  await expect(card.getByText(/Capacità libera del team: .* su .* giorni in \d+ settimane?/)).toBeVisible();
  await card.getByRole("button", { name: "Accetta", exact: true }).click();
  await expect(card).toHaveCount(0);
  const after = await call(page.request, "GET", `/api/v1/elements/${impact.key}`);
  expect(after.impact.state).toBe("Accettato");
});

test("impatto: la stima si inserisce dalla riga e «Rifiuta» chiede il motivo", async ({ page, playwright, baseURL }) => {
  const dm = await apiAs(playwright, baseURL!, "dm");
  const r = await apiAs(playwright, baseURL!, "richiedente");
  const { id: reqId, key: reqKey } = await newRequest(r.req, uniq("Portale fornitori"));
  await r.req.dispose();
  const apps = await call(dm.req, "GET", "/api/v1/architecture/applications?q=");
  const app = apps.items.find((a: { team: string | null }) => a.team === "Web");
  await advance(dm.req, reqId, "Triage");
  const impact = await call(dm.req, "POST", `/api/v1/demand/requests/${reqKey}/impacts`, { application_id: app.id, change_kind: "change" });
  await dm.req.dispose();
  await login(page, "lead");
  await page.goto("/da-fare");
  const card = page.getByRole("listitem", { name: impact.key, exact: true });
  await expect(card.getByText("Nessuna stima")).toBeVisible();
  await card.getByRole("button", { name: "Stima", exact: true }).click();
  await card.getByRole("textbox", { name: "Giorni stimati" }).fill("6,5");
  await card.getByRole("textbox", { name: "Giorni stimati" }).press("Enter");
  await expect(card.getByText("Stima: 6,5 giorni")).toBeVisible();
  const after = await call(page.request, "GET", `/api/v1/elements/${impact.key}`);
  expect(after.impact.estimate_effort_days).toBe(6.5);
});

test("campanello: conta solo ciò che chiede un'azione; gli avvisi stanno nelle Attività, raggruppati, con «Silenzia»", async ({ page, playwright, baseURL }) => {
  const { key, id } = await requestInApproval(playwright, baseURL!, "Cruscotto per il collegio sindacale");
  const dm = await apiAs(playwright, baseURL!, "dm");
  // Una menzione (chiede una risposta) e due commenti senza menzioni (solo avvisi).
  await call(dm.req, "POST", `/api/v1/elements/${id}/comments`, { body: "Ciao @Marta Ferri, serve il tuo parere." });
  await call(dm.req, "POST", `/api/v1/elements/${id}/comments`, { body: "Aggiornamento senza menzioni." });
  await call(dm.req, "POST", `/api/v1/elements/${id}/comments`, { body: "Altro aggiornamento senza menzioni." });
  await dm.req.dispose();
  await login(page, "sponsor");
  const me = await apiAs(playwright, baseURL!, "sponsor");
  const c = await call(me.req, "GET", "/api/v1/todo/count");
  const notes = await call(me.req, "GET", "/api/v1/notifications/count");
  // Il campanello conta le azioni (decisioni e menzioni), non tutte le notifiche non lette.
  expect(c.decisions).toBeGreaterThanOrEqual(1);
  expect(c.mentions).toBeGreaterThanOrEqual(1);
  expect(c.total).toBe(c.decisions + c.reviews + c.verifications + c.impacts + c.time + c.reports + c.mentions);
  expect(notes.unread).toBeGreaterThan(c.mentions);
  await me.req.dispose();
  await page.goto("/richieste");
  const bell = page.getByRole("link", { name: /^Da fare: \d+ cose aspettano te$/ }).first();
  await expect(bell).toBeVisible();
  // La menzione è nella sezione «Menzioni» di Da fare.
  await page.goto("/da-fare");
  const men = page.getByRole("region", { name: /^Menzioni/ });
  await expect(men.getByText("serve il tuo parere", { exact: false })).toBeVisible();
  // Attività: i commenti dello stesso elemento in una sola riga, silenziabile.
  await page.goto("/notifiche");
  await expect(page.getByRole("heading", { name: "Attività", level: 1 })).toBeVisible();
  const group = page.getByRole("listitem").filter({ hasText: key }).filter({ hasText: /\d+ avvisi/ }).first();
  await expect(group).toBeVisible();
  await group.getByRole("button", { name: `Silenzia gli avvisi di ${key}` }).click();
  await expect(group.getByRole("button", { name: `Riattiva gli avvisi di ${key}` })).toBeVisible();
  // Da ora i commenti su quell'elemento non arrivano più.
  // Si contano gli avvisi di quell'elemento (il conteggio generale cambia anche per gli altri test).
  const forKey = async () => (await call(page.request, "GET", "/api/v1/notifications?limit=200")).items.filter((n: { element_key: string }) => n.element_key === key).length;
  const before = await forKey();
  const dm2 = await apiAs(playwright, baseURL!, "dm");
  await call(dm2.req, "POST", `/api/v1/elements/${id}/comments`, { body: "Commento dopo il silenzio." });
  await dm2.req.dispose();
  expect(await forKey()).toBe(before);
  await group.getByRole("button", { name: `Riattiva gli avvisi di ${key}` }).click();
  await expect(group.getByRole("button", { name: `Silenzia gli avvisi di ${key}` })).toBeVisible();
});

test("stato vuoto utile e vecchie code che rimandano a Da fare", async ({ page, playwright, baseURL }) => {
  // Chi non ha niente da fare: si cerca tra gli accessi di prova quello a zero (nessun dato condiviso si tocca).
  let who = "";
  for (const w of ["finanza", "arch", "compliance", "conformita", "dev2", "rischi2"]) {
    const a = await apiAs(playwright, baseURL!, w);
    const n = await total(a.req);
    await a.req.dispose();
    if (n === 0) {
      who = w;
      break;
    }
  }
  test.skip(!who, "nessun accesso di prova senza voci in Da fare");
  await login(page, who);
  await page.goto("/da-fare");
  await expect(page.getByRole("heading", { name: "Hai finito." })).toBeVisible();
  await expect(page.getByText(/Prossima seduta di .*:|Nessuna seduta in programma\./)).toBeVisible();
  // Le vecchie code restano raggiungibili e rimandano qui.
  await page.goto("/approvazioni");
  await expect(page.getByRole("heading", { name: "Le mie approvazioni" })).toBeVisible();
  await page.getByRole("link", { name: /Vai a Da fare/ }).click();
  await expect(page).toHaveURL(/\/da-fare$/);
  // Il menu ha «Da fare» come prima voce.
  const nav = page.getByRole("navigation", { name: "Navigazione principale" });
  await expect(nav.getByRole("link", { name: /^Da fare\d*\+?$/ })).toBeVisible();
  await expect(nav.getByRole("link", { name: "Le mie approvazioni" })).toHaveCount(0);
});

test.describe("telefono (375 px)", () => {
  test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

  test("carte con pulsanti da 44 px, senza scorrimento laterale", async ({ page, playwright, baseURL }) => {
    const { key, title } = await requestInApproval(playwright, baseURL!, "Registro delle consegne");
    await login(page, "sponsor");
    await page.goto("/da-fare");
    const card = page.getByRole("listitem", { name: key, exact: true });
    await expect(card).toBeVisible();
    await expectNoHorizontalScroll(page);
    for (const name of ["Approva", "Con condizioni", "Rinvia", "Respingi"]) {
      const box = await card.getByRole("button", { name, exact: true }).boundingBox();
      expect(box!.height, name).toBeGreaterThanOrEqual(43.5);
    }
    const link = card.getByRole("link", { name: title });
    expect((await link.boundingBox())!.height).toBeGreaterThanOrEqual(43.5);
    // Decisione dal telefono.
    await card.getByRole("button", { name: "Rinvia", exact: true }).click();
    await card.getByRole("textbox", { name: "Motivazione" }).fill("Ne riparliamo il mese prossimo.");
    await card.getByRole("textbox", { name: "Motivazione" }).press("Enter");
    await expect(card).toHaveCount(0);
    await expectNoHorizontalScroll(page);
    // Il campanello (barra in alto) è da 44 px.
    const bell = page.getByRole("link", { name: /^Da fare: / }).first();
    expect((await bell.boundingBox())!.height).toBeGreaterThanOrEqual(43.5);
  });
});

test.describe("in inglese", () => {
  // La lingua viene dal browser: salvare la preferenza dell'accesso condiviso «sponsor» cambierebbe
  // la lingua degli altri test in parallelo.
  test.use({ locale: "en-US" });
  test("la pagina e la decisione in linea parlano inglese", async ({ page, playwright, baseURL }) => {
    const { key, title } = await requestInApproval(playwright, baseURL!, "Retention policy review");
    await login(page, "sponsor");
    {
      await page.goto("/da-fare");
      await expect(page.getByRole("heading", { name: "To do", level: 1 })).toBeVisible();
      const card = page.getByRole("listitem", { name: key, exact: true });
      await expect(card.getByRole("link", { name: title })).toBeVisible();
      await expect(card.getByText(/Amount 30k €|Amount 30 k€/)).toBeVisible();
      for (const name of ["Approve", "With conditions", "Defer", "Reject"]) {
        await expect(card.getByRole("button", { name, exact: true })).toBeVisible();
      }
      await card.getByRole("button", { name: "With conditions", exact: true }).click();
      await card.getByRole("textbox", { name: "Conditions" }).fill("Only after the security review.");
      await card.getByRole("button", { name: "Send", exact: true }).click();
      await expect(card).toHaveCount(0);
      await expect(page.getByText(`${key}: decision recorded, approved with conditions.`)).toBeVisible();
      await expect(page.getByRole("link", { name: /^To do: / }).first()).toBeVisible();
    }
  });
});

// ---------------------------------------------------------------------
// Quasi vittorie dell'area lavoro (ondata 2)

test("richieste: «Prendi in carico» dalla riga e selezione multipla con comando in blocco", async ({ page, playwright, baseURL }) => {
  const r = await apiAs(playwright, baseURL!, "richiedente");
  const a = await newRequest(r.req, uniq("Cassetto fiscale aziendale"));
  const b = await newRequest(r.req, uniq("Archivio delle polizze"));
  const c = await newRequest(r.req, uniq("Portale dei fornitori"));
  await r.req.dispose();
  const dm = await apiAs(playwright, baseURL!, "dm");
  const meId = dm.me.active_tenant!.member_id;
  await login(page, "dm");
  // L'invito e il pulsante di riga sono della vista elenco; la tabella è la vista predefinita.
  await page.goto("/richieste?vista=lista");
  await expect(page.getByRole("heading", { name: "Richieste", level: 1 })).toBeVisible();
  // L'invito alla selezione e la colonna del responsabile.
  await expect(page.getByText("Seleziona più richieste per agire su tutte insieme", { exact: false })).toBeVisible();
  const rowA = page.getByRole("row", { name: new RegExp(a.key + " ") });
  await expect(rowA).toBeVisible();
  await rowA.getByRole("button", { name: "Prendi in carico" }).click();
  await expect(page.getByText(`${a.key}: preso in carico.`)).toBeVisible();
  await expect(rowA.getByRole("button", { name: "Prendi in carico" })).toHaveCount(0);
  expect((await call(dm.req, "GET", `/api/v1/elements/${a.key}`)).element.assignee_id).toBe(meId);

  // Selezione di due richieste e «Prendi in carico» in blocco, con anteprima.
  await page.getByRole("checkbox", { name: `Seleziona ${b.key}` }).click();
  await page.getByRole("checkbox", { name: `Seleziona ${c.key}` }).click();
  const bar = page.getByRole("region", { name: "Comandi in blocco" });
  await expect(bar.getByText("2 selezionati")).toBeVisible();
  // Per le richieste non ci sono sprint né eliminazione.
  await expect(bar.getByRole("button", { name: "Elimina" })).toHaveCount(0);
  await bar.getByRole("button", { name: "Prendi in carico", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "Anteprima del comando" })).toBeVisible();
  await page.getByRole("button", { name: "Applica a 2" }).click();
  await expect(page.getByRole("status").filter({ hasText: /^Fatto:/ })).toBeVisible();
  for (const k of [b.key, c.key]) {
    expect((await call(dm.req, "GET", `/api/v1/elements/${k}`)).element.assignee_id, k).toBe(meId);
  }
  await dm.req.dispose();
});

test("portale: unità precompilata dal profilo, una sola bozza e richieste simili mentre si scrive", async ({ page, playwright, baseURL }) => {
  const admin = await apiAs(playwright, baseURL!, "admin");
  const units = await call(admin.req, "GET", "/api/v1/org/units");
  const root = units.items.find((u: { parent_id: string | null }) => !u.parent_id);
  const unitName = uniq("Area sinistri");
  const unit = await call(admin.req, "POST", "/api/v1/org/units", { name: unitName, parent_id: root.id, kind: "business_unit" });
  const who = await apiAs(playwright, baseURL!, "dev2");
  const memberId = who.me.active_tenant!.member_id;
  await call(admin.req, "PUT", `/api/v1/org/units/${unit.id}/members/${memberId}`, { role: "member" });
  await admin.req.dispose();
  // Una richiesta già aperta dalla stessa persona, con parole in comune.
  const word = `zefiro${Date.now().toString(36)}`;
  const existing = await newRequest(who.req, uniq(`Modulo reclami ${word} clienti online`));

  // Una sola bozza per tipo: due salvataggi senza id danno la stessa bozza.
  const d1 = await call(who.req, "POST", "/api/v1/portal/drafts", { type: "business_request", title: "Prima stesura", step: 0 });
  const d2 = await call(who.req, "POST", "/api/v1/portal/drafts", { type: "business_request", title: "Seconda stesura", step: 0 });
  expect(d2.id).toBe(d1.id);
  const portal = await call(who.req, "GET", "/api/v1/portal");
  expect(portal.drafts.filter((d: { type: string }) => d.type === "business_request")).toHaveLength(1);
  // La radice (l'azienda intera) non è un'unità richiedente; quella della persona è già indicata.
  expect(portal.my_unit).toBe(unit.id);
  expect(portal.units.some((u: { key: string }) => u.key === root.id)).toBe(false);

  try {
    await login(page, "dev2");
    await page.goto("/portale/nuova");
    // Riprende la bozza invece di ricominciare, e lo dice.
    await expect(page.getByText("Hai già una bozza: ripresa da dove l'avevi lasciata.")).toBeVisible();
    const titolo = page.getByRole("textbox", { name: /In una riga, cosa ti serve/ });
    await expect(titolo).toHaveValue("Seconda stesura");
    // Richieste simili mentre si scrive.
    await titolo.fill(`Nuovo modulo reclami ${word} per i clienti`);
    const simili = page.getByRole("region", { name: "Richieste simili già aperte" });
    await expect(simili).toBeVisible();
    await expect(simili.getByRole("link", { name: new RegExp(existing.key) })).toBeVisible();
    // L'unità è già scelta.
    await page.getByRole("button", { name: "Avanti" }).click();
    await expect(page.getByRole("heading", { name: "Tempi e contesto", level: 2 })).toBeVisible();
    await expect(page.getByRole("combobox", { name: /Per quale unità/ })).toHaveAttribute("data-value", unit.id);
    await expect(page.getByText("Unità precompilata dal tuo profilo.")).toBeVisible();
  } finally {
    await call(who.req, "DELETE", `/api/v1/portal/drafts/${d1.id}`);
    await who.req.dispose();
  }
});

test("backlog: creazione rapida in fondo allo sprint e sprint scelto nel dialogo", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const team = uniq("Team backlog rapido");
  await call(req, "POST", "/api/v1/teams", { name: team });
  const spaces = await call(req, "GET", "/api/v1/work/spaces");
  const space = spaces.items.find((s: { team: string }) => s.team === team).key as string;
  await call(req, "POST", `/api/v1/work/spaces/${space}/sprints`, { title: "Sprint rapido", goal: "Provare la creazione in linea" });
  await login(page, "admin");
  await page.goto(`/delivery/backlog?spazio=${space}`);
  await expect(page.getByRole("heading", { name: "Backlog e sprint" })).toBeVisible();
  // Titolo e Invio in fondo allo sprint: l'elemento nasce già dentro.
  const rapido = page.getByRole("textbox", { name: "Aggiungi un elemento a Sprint rapido" });
  await rapido.fill("Voce scritta in linea");
  await rapido.press("Enter");
  await expect(page.getByText(/ aggiunto\.$/)).toBeVisible();
  const sezione = page.getByRole("region", { name: "Sprint rapido" });
  await expect(sezione.getByText("Voce scritta in linea")).toBeVisible();
  // «Nuovo elemento»: lo sprint si sceglie nel dialogo.
  await page.getByRole("button", { name: "Nuovo elemento" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("textbox", { name: /Titolo/ }).fill("Voce dal dialogo");
  await scegli(dialog.getByRole("combobox", { name: "Sprint" }), /Sprint rapido/);
  await dialog.getByRole("button", { name: "Crea" }).click();
  await expect(sezione.getByText("Voce dal dialogo")).toBeVisible();
  await req.dispose();
});
