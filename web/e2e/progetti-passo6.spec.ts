// Progetti, passo 6: la richiesta confluita rimanda agli status report del progetto, la scheda «Progetti»
// dell'applicazione con i conflitti, la dipendenza da una milestone di un altro progetto che slitta (piano,
// Panoramica e roadmap del programma), la mappa dei semafori con i filtri e la roadmap di portafoglio con i
// progetti, il contratto del fornitore dal registro ICT. I dati si preparano via API; date lontane perché
// altri test non si sovrappongano.
import { test, expect } from "@playwright/test";
import { advance, apiAs, call, expectNoHorizontalScroll, login, newRequest, scegli, uniq } from "./fixtures";

type Item = { id: string; key: string; title: string };
const day = (offset: number) => new Date(Date.now() + offset * 86_400_000).toISOString().slice(0, 10);

test("la richiesta confluita nel progetto mostra l'ultimo status report del progetto; i suoi report restano", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const { id, key } = await newRequest(req, uniq("Report sulla richiesta"));
  await req.dispose();
  const dm = await apiAs(playwright, baseURL!, "dm");
  await advance(dm.req, id, "Triage", "Valutazione");
  // Un report della richiesta, prima del progetto.
  const old = await call<{ id: string }>(dm.req, "POST", `/api/v1/demand/requests/${key}/status-reports`, {});
  await call(dm.req, "POST", `/api/v1/status-reports/${old.id}/submit`);
  await call(dm.req, "PATCH", `/api/v1/demand/requests/${id}`, { estimate_low_cents: 1_000_000, estimate_high_cents: 2_000_000 });
  await advance(dm.req, id, "Prioritizzazione");
  const el = await call(dm.req, "GET", `/api/v1/elements/${id}`);
  await call(dm.req, "PATCH", `/api/v1/elements/${id}`, { version: el.element.version, fields: { data_golive: "2031-05-31" } });
  await advance(dm.req, id, "Approvazione");
  await dm.req.dispose();
  const sp = await apiAs(playwright, baseURL!, "sponsor");
  const inbox = await call<{ items: { step_id: string; element_key: string }[] }>(sp.req, "GET", "/api/v1/approvals/inbox");
  const step = inbox.items.find((i) => i.element_key === key)!;
  expect(step, "decisione dello sponsor in attesa").toBeTruthy();
  await call(sp.req, "POST", `/api/v1/approvals/steps/${step.step_id}/decision`, { decision: "approved", comment: "Approvata dai test." });
  await sp.req.dispose();

  const admin = await apiAs(playwright, baseURL!, "admin");
  const { project } = await call<{ project: { key: string } }>(admin.req, "GET", `/api/v1/demand/requests/${key}/project`);
  expect(project, "progetto nato dalla richiesta").toBeTruthy();
  const sr = await call<{ id: string; version: number; sections: Record<string, unknown> }>(admin.req, "POST", `/api/v1/projects/${project.key}/status-reports`, {});
  await call(admin.req, "PATCH", `/api/v1/project-reports/${sr.id}`, { version: sr.version, sections: { ...sr.sections, summary: "Il progetto parte con il piano approvato." }, overall: "amber" });
  await call(admin.req, "POST", `/api/v1/project-reports/${sr.id}/submit`);
  await admin.req.dispose();

  await login(page, "dm");
  await page.goto(`/e/${key}?scheda=stato`);
  const box = page.getByTestId("stato-progetto");
  await expect(box).toContainText(`La richiesta è confluita nel progetto ${project.key}`);
  await expect(box).toContainText("Il progetto parte con il piano approvato.");
  await expect(box).toContainText("Giallo");
  await expect(page.getByRole("button", { name: "Nuovo status report" })).toHaveCount(0);
  await expect(page.getByText("Report della richiesta, prima del progetto")).toBeVisible();
  await box.getByRole("link", { name: "Apri lo status report del progetto" }).click();
  await expect(page).toHaveURL(new RegExp(`/e/${project.key}\\?scheda=stato`));
  await expect(page.getByText("Il progetto parte con il piano approvato.").first()).toBeVisible();
});

test("la scheda «Progetti» dell'applicazione mostra i work package nel tempo e il conflitto", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const code = uniq("APP").replace(/[^A-Za-z0-9]/g, "").slice(-10).toUpperCase();
  await call(req, "POST", "/api/v1/architecture/applications/import", { csv: `codice,nome\n${code},Applicazione ${code}\n` });
  const apps = await call<{ items: { id: string; key: string; code: string }[] }>(req, "GET", "/api/v1/architecture/applications");
  const app = apps.items.find((a) => a.code === code)!;
  const a = await call<Item>(req, "POST", "/api/v1/projects", { title: uniq("Canali A"), template: "agile", no_phases: true });
  const b = await call<Item>(req, "POST", "/api/v1/projects", { title: uniq("Canali B"), template: "agile", no_phases: true });
  await call(req, "POST", `/api/v1/projects/${a.key}/items`, { kind: "work_package", title: "Secondo fattore", application_id: app.id, start: "2031-07-07", end: "2031-08-01" });
  await call(req, "POST", `/api/v1/projects/${b.key}/items`, { kind: "work_package", title: "Nuovo login", application_id: app.id, start: "2031-07-21", end: "2031-08-15" });
  await req.dispose();

  await login(page, "admin");
  await page.goto(`/e/${app.key}?scheda=progetti`);
  const tab = page.getByTestId("applicazione-progetti");
  await expect(tab.getByTestId("applicazione-wp")).toHaveCount(2);
  await expect(tab).toContainText("Secondo fattore");
  await expect(tab).toContainText("Nuovo login");
  const conflicts = page.getByTestId("applicazione-conflitti");
  await expect(conflicts).toContainText(a.key);
  await expect(conflicts).toContainText(b.key);
  await expect(conflicts).toContainText("dal 21/07/2031 al 01/08/2031");
  // Sul telefono: elenco senza scorrimento orizzontale.
  await page.setViewportSize({ width: 375, height: 800 });
  await expect(tab.getByTestId("applicazione-wp-mobile")).toHaveCount(2);
  await expectNoHorizontalScroll(page);
});

test("dipendenza da una milestone di un altro progetto: il piano la mostra, lo slittamento accende il semaforo e la roadmap del programma", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const pg = await call<Item>(req, "POST", "/api/v1/programs", { title: uniq("Programma dipendenze") });
  const a = await call<Item>(req, "POST", "/api/v1/projects", { title: uniq("Identità"), template: "agile", no_phases: true });
  const b = await call<Item>(req, "POST", "/api/v1/projects", { title: uniq("Portale"), template: "agile", no_phases: true });
  for (const p of [a, b]) await call(req, "PUT", `/api/v1/projects/${p.key}/program`, { program: pg.key });
  const ms = await call<Item>(req, "POST", `/api/v1/projects/${a.key}/items`, { kind: "milestone", title: uniq("IDP pronto"), date: "2031-09-12" });
  const wp = await call<Item>(req, "POST", `/api/v1/projects/${b.key}/items`, { kind: "work_package", title: "Login con secondo fattore", start: "2031-09-22", end: "2031-10-10" });

  await login(page, "admin");
  await page.goto(`/progetti/${b.key}/piano`);
  const panel = page.getByTestId("dipendenze-progetti");
  await panel.getByRole("button", { name: "Aggiungi una dipendenza" }).click();
  const form = panel.getByRole("form", { name: "Aggiungi una dipendenza" });
  await scegli(form.getByRole("combobox", { name: "Milestone di un altro progetto" }), new RegExp(ms.title));
  await scegli(form.getByRole("combobox", { name: "Voce che dipende" }), new RegExp(wp.key));
  await expect(form).toContainText("Senza data vale l'inizio della voce: 22/09/2031.");
  await form.getByRole("button", { name: "Salva la dipendenza" }).click();
  const dep = panel.getByTestId("dipendenza");
  await expect(dep).toContainText(ms.title);
  await expect(dep).toContainText("serve entro il 22/09/2031 · prevista il 12/09/2031");
  await expect(dep).toContainText("in tempo");
  await expect(page.getByTestId("dipendenza-chip").first()).toContainText(a.key);

  // La milestone slitta oltre la data che serve: il progetto B lo segnala.
  await call(req, "PUT", `/api/v1/plan-items/${ms.id}/schedule`, { start: "2031-09-26", end: "2031-09-26" });
  await page.reload();
  await expect(panel.getByTestId("dipendenza")).toContainText("in ritardo di 4 giorni lavorativi");
  await page.goto(`/e/${b.key}?scheda=panoramica`);
  await expect(page.getByTestId("semaforo")).toContainText("Una milestone di un altro progetto arriva dopo la data che serve");
  await expect(page.getByTestId("panoramica-dipendenze")).toContainText(ms.title);
  // Il piano del progetto A vede chi aspetta la sua milestone.
  await page.goto(`/progetti/${a.key}/piano`);
  await expect(page.getByTestId("dipendenti")).toContainText(b.key);
  // La roadmap del programma mostra la dipendenza in ritardo.
  await page.goto(`/e/${pg.key}?scheda=panoramica`);
  const roadmap = page.getByRole("group", { name: /Roadmap/ });
  await expect(roadmap.getByTestId("roadmap-dipendenza")).toHaveAttribute("aria-label", /in ritardo di 4 giorni lavorativi/);
  await req.dispose();
});

test("mappa dei semafori del portafoglio con i filtri e roadmap di portafoglio con i progetti", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const label = uniq("Mappa");
  const late = await call<Item>(req, "POST", "/api/v1/projects", { title: `${label} in ritardo`, template: "agile", no_phases: true });
  const ok = await call<Item>(req, "POST", "/api/v1/projects", { title: `${label} in tempo`, template: "agile", no_phases: true });
  await call(req, "POST", `/api/v1/projects/${late.key}/items`, { kind: "work_package", title: "Scaduto", start: day(-30), end: day(-10) });
  await call(req, "POST", `/api/v1/projects/${ok.key}/items`, { kind: "work_package", title: "Futuro", start: "2031-11-03", end: "2031-11-28" });
  await req.dispose();

  await login(page, "admin");
  await page.goto("/progetti");
  await page.getByRole("tab", { name: "Semafori" }).click();
  const map = page.getByTestId("mappa-semafori");
  await map.getByRole("searchbox", { name: "Cerca" }).fill(label);
  const rows = map.getByTestId("mappa-progetto");
  await expect(rows).toHaveCount(2);
  const lateRow = map.locator(`[data-testid="mappa-progetto"][data-key="${late.key}"]`);
  await expect(lateRow.locator('[data-area="schedule"]')).toContainText("Giallo");
  await expect(map.locator(`[data-testid="mappa-progetto"][data-key="${ok.key}"] [data-area="schedule"]`)).toContainText("Verde");
  // Tutti e due sono gialli nel complesso (mancano i ruoli obbligatori): l'ambito separa quello in ritardo.
  await scegli(map.getByRole("combobox", { name: "Ambito con problemi" }), "Tempi");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toHaveAttribute("data-key", late.key);
  await scegli(map.getByRole("combobox", { name: "Semaforo" }), "Solo rossi");
  await expect(rows).toHaveCount(0);
  await expect(map).toContainText("Nessun progetto corrisponde ai filtri.");

  // Sul telefono la mappa è un elenco, senza scorrimento orizzontale.
  await page.setViewportSize({ width: 375, height: 800 });
  await expectNoHorizontalScroll(page);
  await page.setViewportSize({ width: 1400, height: 900 });

  // La roadmap di portafoglio mostra i progetti accanto alle richieste.
  await page.goto("/roadmap");
  const projects = page.getByTestId("roadmap-progetti");
  await expect(projects).toContainText(late.key);
  await expect(projects).toContainText(ok.key);
  await expect(page.getByText("Richieste non ancora diventate progetto")).toBeVisible();
});

test("il work package del fornitore si collega a un contratto del registro ICT e si scollega", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const provider = await call<{ id: string; key: string }>(req, "POST", "/api/v1/ict/providers", { name: uniq("Fornitore IDP") });
  const ref = uniq("CTR").replace(/\s/g, "-");
  await call(req, "POST", `/api/v1/ict/providers/${provider.key}/contracts`, { reference: ref, annual_cost_cents: 2_400_000, currency: "EUR", start_date: "2031-01-01", end_date: "2033-12-31" });
  const p = await call<Item>(req, "POST", "/api/v1/projects", { title: uniq("Commessa"), template: "contract", no_phases: true });
  const wp = await call<Item>(req, "POST", `/api/v1/projects/${p.key}/items`, {
    kind: "work_package", title: "Integrazione IDP", delivery_mode: "supplier", supplier_id: provider.id, start: "2031-03-03", end: "2031-03-28",
  });
  await req.dispose();

  await login(page, "admin");
  await page.goto(`/e/${wp.key}?scheda=dettagli`);
  const reg = page.getByTestId("contratto-registro");
  await scegli(reg.getByRole("combobox", { name: "Contratto dal registro ICT" }), new RegExp(ref));
  await reg.getByRole("button", { name: "Collega" }).click();
  const linked = page.getByTestId("contratto-collegato");
  await expect(linked).toContainText(ref);
  await expect(linked).toContainText("01/01/2031 – 31/12/2033");
  await expect(linked).toContainText("Costo annuo");
  // I campi del work package restano liberi: il contratto collegato fa da segnaposto.
  await expect(page.getByRole("textbox", { name: "Riferimento del contratto" })).toHaveAttribute("placeholder", ref);
  await linked.getByRole("button", { name: "Scollega il contratto" }).click();
  await expect(page.getByTestId("contratto-collegato")).toHaveCount(0);
  await expect(page.getByTestId("contratto-registro")).toBeVisible();
});
