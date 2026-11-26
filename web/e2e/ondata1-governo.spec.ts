// Ondata 1 del governo (replica/usabilita.md, sezione 4): incidenti (banda della prossima
// notifica, scheda «Dettagli», risoluzione che porta la fase, invito a chiudere, causa come
// fornitore), heatmap con elenco e frecce, gate (un solo stato per rilascio, override dal motivo),
// vulnerabilità sul telefono, copertura pesata e scheda del controllo, codice di catalogo,
// etichette dei nodi della mappa. Con casi a 375 px.
import { test, expect, type APIRequestContext } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, uniq, scegli } from "./fixtures";

const MOBILE = { width: 375, height: 812 };

/** Incidente grave già classificato (con le notifiche normative), creato come gestore degli incidenti. */
async function majorIncident(req: APIRequestContext, title: string) {
  const inc = await call<{ key: string; element: { id: string } }>(req, "POST", "/api/v1/incidents", {
    title,
    description: "Creato dai test end-to-end.",
    detected_at: new Date(Date.now() - 2 * 3_600_000).toISOString(),
    clients_affected: 180000,
    clients_affected_pct: 14,
    downtime_minutes: 190,
    economic_impact_cents: 21000000,
    cause: "system",
  });
  await call(req, "POST", `/api/v1/elements/${inc.element.id}/transitions`, { to: "Classificato" });
  return { key: inc.key, id: inc.element.id };
}

test("incidente: si apre su «Dettagli» con la banda della prossima notifica", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "incidenti");
  const inc = await majorIncident(req, uniq("Banda notifica"));
  await login(page, "incidenti");
  await page.goto(`/e/${inc.key}`);
  await expect(page.getByRole("tab", { name: "Dettagli" })).toHaveAttribute("aria-selected", "true");
  const band = page.getByLabel("Da fare sull'incidente");
  await expect(band.getByText("Prossima notifica")).toBeVisible();
  await expect(band.getByRole("timer")).toContainText(/mancano|scaduta/);
  // La scheda «Attività» resta raggiungibile e la scelta si ricorda nell'indirizzo.
  await page.getByRole("tab", { name: "Attività" }).click();
  await expect(page).toHaveURL(/scheda=attivita/);
  await expect(page.getByRole("tab", { name: "Attività" })).toHaveAttribute("aria-selected", "true");
});

test("incidente a 375 px: banda e «Prepara la notifica» subito raggiungibili, bersagli da 44 px", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "incidenti");
  const inc = await majorIncident(req, uniq("Banda telefono"));
  await page.setViewportSize(MOBILE);
  await login(page, "incidenti");
  await page.goto(`/e/${inc.key}`);
  const band = page.getByLabel("Da fare sull'incidente");
  await expect(band.getByText("Prossima notifica")).toBeInViewport();
  const prepare = band.getByRole("button", { name: "Prepara la notifica" });
  await expect(prepare).toBeInViewport();
  const box = await prepare.boundingBox();
  expect(box!.height, "altezza del bersaglio").toBeGreaterThanOrEqual(44);
  await expectNoHorizontalScroll(page);
  // Il pulsante porta alle notifiche, dove si prepara l'invio.
  await prepare.click();
  await expect(page.getByRole("region", { name: "Notifiche normative" })).toBeInViewport();
});

test("incidente: registrare la risoluzione porta la fase, o chiede quale fase", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "incidenti");
  // Da «Classificato» il processo porta a «Risolto»: la fase cambia da sola.
  const a = await majorIncident(req, uniq("Risoluzione diretta"));
  await call(req, "PATCH", `/api/v1/incidents/${a.key}`, { resolved_at: new Date(Date.now() - 3_600_000).toISOString() });
  const after = await call<{ element: { state: string } }>(req, "GET", `/api/v1/elements/${a.key}`);
  expect(after.element.state).toBe("Risolto");

  // Da «Rilevato» no: l'interfaccia chiede in quale fase portarlo.
  const inc = await call<{ key: string }>(req, "POST", "/api/v1/incidents", { title: uniq("Risoluzione guidata"), detected_at: new Date(Date.now() - 5 * 3_600_000).toISOString() });
  await login(page, "incidenti");
  await page.goto(`/e/${inc.key}`);
  await page.getByRole("button", { name: "Modifica" }).first().click();
  const resolved = new Date(Date.now() - 3_600_000);
  const local = new Date(resolved.getTime() - resolved.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
  await page.getByLabel("Risolto il").fill(local);
  await page.getByRole("button", { name: "Salva" }).click();
  const panel = page.getByRole("status", { name: "Fase dopo la risoluzione" });
  await expect(panel).toContainText("Risoluzione registrata");
  await panel.getByRole("button", { name: /Classificato/ }).click();
  await expect(panel).toHaveCount(0);
  await expect(page.getByText("Classificato").first()).toBeVisible();
});

test("incidente risolto ma fermo in una fase: l'invito a portarlo avanti, anche a 375 px", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "incidenti");
  const inc = await call<{ key: string }>(req, "POST", "/api/v1/incidents", { title: uniq("Risolto e fermo"), detected_at: new Date(Date.now() - 4 * 86_400_000).toISOString() });
  // «Rilevato» non porta direttamente a «Risolto»: la data resta e la fase no.
  await call(req, "PATCH", `/api/v1/incidents/${inc.key}`, { resolved_at: new Date(Date.now() - 3 * 86_400_000).toISOString() });
  await page.setViewportSize(MOBILE);
  await login(page, "incidenti");
  await page.goto(`/e/${inc.key}`);
  const band = page.getByLabel("Da fare sull'incidente");
  await expect(band).toContainText("ma l'incidente è ancora in fase «Rilevato»");
  const next = band.getByRole("button", { name: "Classifica" });
  expect((await next.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await next.click();
  await expect(band).toContainText("ancora in fase «Classificato»");
  await expectNoHorizontalScroll(page);
});

test("«Causa: fornitore terzo» si sceglie dall'elenco dei fornitori", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "incidenti");
  const inc = await call<{ key: string }>(req, "POST", "/api/v1/incidents", { title: uniq("Causa fornitore"), detected_at: new Date(Date.now() - 3_600_000).toISOString() });
  await login(page, "incidenti");
  await page.goto(`/e/${inc.key}`);
  await page.getByRole("button", { name: "Modifica" }).first().click();
  const impact = page.getByRole("region", { name: "Impatto" });
  const providerSelect = impact.getByRole("combobox", { name: /Fornitore/ });
  await expect(providerSelect).toHaveCount(0);
  await scegli(impact.getByRole("combobox", { name: /Causa/ }), { value: "provider" });
  await expect(providerSelect).toBeVisible();
  await scegli(providerSelect, { index: 1 });
  const name = ((await providerSelect.textContent()) ?? "").replace(/\s*\(.*\)$/, "").trim();
  await page.getByRole("button", { name: "Salva" }).click();
  await expect(page.getByText("fornitore terzo", { exact: false }).first()).toBeVisible();
  await expect(page.getByRole("link", { name })).toBeVisible();
  // Cambiando causa il fornitore sparisce.
  const view = await call<{ cause_provider: { title: string } | null }>(req, "GET", `/api/v1/incidents/${inc.key}`);
  expect(view.cause_provider?.title).toBe(name);
  await call(req, "PATCH", `/api/v1/incidents/${inc.key}`, { cause: "cyber" });
  expect((await call<{ cause_provider: unknown }>(req, "GET", `/api/v1/incidents/${inc.key}`)).cause_provider).toBeNull();
});

test("heatmap: il clic su una cella elenca i rischi, con le frecce da inerente a residuo", async ({ page }) => {
  await login(page, "rischi");
  await page.goto("/rischi");
  await expect(page.getByRole("heading", { level: 1, name: "Rischi e incidenti" })).toBeVisible();
  const cell = page.locator('svg g[role="button"][tabindex="0"]').first();
  await expect(cell).toBeVisible();
  await cell.click();
  const list = page.getByLabel(/^Rischi della cella/);
  await expect(list).toBeVisible();
  const rows = list.locator("li");
  await expect(rows.first()).toBeVisible();
  await expect(rows.first().getByLabel("Da inerente a residuo")).toContainText("→");
  await expect(rows.first().getByRole("link")).toHaveAttribute("href", /\/e\/RIS-\d+/);
  // Nella vista inerente le frecce vanno al residuo: ce n'è almeno una dove il trattamento ha ridotto il rischio.
  await page.getByRole("button", { name: "inerente" }).click();
  await page.locator('svg g[role="button"][tabindex="0"]').first().click();
  await expect(page.getByLabel(/^Rischi della cella/)).toBeVisible();
});

test("gate: un solo stato per rilascio e «Chiedi override» parte dal motivo del blocco", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/devsecops");
  const gate = page.getByRole("region", { name: "Gate di rilascio" });
  await expect(gate.getByRole("heading", { name: "Gate di rilascio" })).toBeVisible();
  // Le decisioni precedenti dello stesso rilascio stanno nella cronologia, non accanto allo stato.
  const history = gate.locator("details").filter({ hasText: "Cronologia di questo rilascio" }).first();
  await expect(history).toBeVisible();
  await expect(gate.getByText("superata", { exact: true }).first()).toBeHidden();
  await history.locator("summary").click();
  await expect(history.getByText("superata", { exact: true }).first()).toBeVisible();
  // Il blocco ha le azioni sul motivo.
  const override = gate.getByRole("button", { name: "Chiedi override" }).first();
  await expect(override).toBeVisible();
  await override.click();
  await expect(gate.getByLabel("Perché si rilascia lo stesso")).toHaveValue(/Si rilascia lo stesso nonostante/);
});

test("vulnerabilità a 375 px: indicatori e «oltre la scadenza» prima dei filtri", async ({ page }) => {
  await page.setViewportSize(MOBILE);
  await login(page, "sicurezza");
  await page.goto("/devsecops/vulnerabilita");
  const summary = page.getByRole("region", { name: "In sintesi" });
  await expect(summary).toBeVisible();
  await expect(summary).toBeInViewport();
  // Le posizioni si misurano a pagina ferma: l'ingresso della pagina (shell.tsx) la sposta di 6 px per 0,25 s.
  await page.waitForFunction(() => document.getAnimations().every((a) => a.playState !== "running"));
  // Il primo indicatore è «Oltre la scadenza»: sta in alto a sinistra, prima di «Da correggere».
  const overdue = (await summary.getByRole("button", { name: /^Oltre la scadenza/ }).boundingBox())!;
  const open = (await summary.getByRole("button", { name: /^Da correggere/ }).boundingBox())!;
  expect(overdue.y).toBeLessThanOrEqual(open.y);
  expect(overdue.x).toBeLessThan(open.x);
  const top = (await summary.boundingBox())!.y;
  const filtersButton = page.getByRole("button", { name: /^Filtri/ });
  await expect(filtersButton).toBeVisible();
  expect((await filtersButton.boundingBox())!.y, "i filtri vengono dopo gli indicatori").toBeGreaterThan(top);
  expect((await filtersButton.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await expect(page.locator("#filtri-vulnerabilita")).toBeHidden();
  await filtersButton.click();
  await expect(page.locator("#filtri-vulnerabilita")).toBeVisible();
  await expectNoHorizontalScroll(page);
});

test("conformità: copertura pesata per criticità e scheda del controllo su «Conformità»", async ({ page }) => {
  await login(page, "conformita");
  await page.goto("/conformita");
  await expect(page.getByText("Copertura pesata per criticità").first()).toBeVisible();
  const dash = await page.request.get("/api/v1/grc/dashboard");
  const tenant = ((await dash.json()) as { tenant: { percent: number; weighted_percent: number; applicable: number } }).tenant;
  expect(tenant.applicable).toBeGreaterThan(0);
  expect(tenant.weighted_percent).toBeGreaterThanOrEqual(0);
  expect(tenant.weighted_percent).toBeLessThanOrEqual(100);
  // La scheda del controllo si apre su «Conformità».
  const found = await page.request.get(`/api/v1/elements?q=${encodeURIComponent("tipo: control")}`);
  const key = ((await found.json()) as { items: { key: string }[] }).items[0].key;
  await page.goto(`/e/${key}`);
  await expect(page.getByRole("tab", { name: "Conformità" })).toHaveAttribute("aria-selected", "true");
});

test("architettura: codice di catalogo in evidenza, chiave piccola accanto, valida in link e ricerca", async ({ page }) => {
  await login(page, "arch");
  // Il codice vale come la chiave: link e ricerca.
  const byCode = await page.request.get("/api/v1/elements/APP-MOB");
  expect(byCode.ok()).toBeTruthy();
  const el = ((await byCode.json()) as { element: { key: string } }).element;
  const search = await page.request.get(`/api/v1/elements?q=${encodeURIComponent("app-mob")}`);
  expect(((await search.json()) as { items: { key: string }[] }).items.map((i) => i.key)).toContain(el.key);

  await page.goto("/e/APP-MOB");
  await expect(page.getByRole("article")).toContainText("APP-MOB");
  await expect(page.getByRole("article").getByTitle(/Chiave dell'elemento/).first()).toHaveText(el.key);

  await page.goto("/architettura?tab=applicazioni");
  const row = page.getByRole("row").filter({ hasText: "APP-MOB" });
  await expect(row).toBeVisible();
  await expect(row.getByTitle(/Chiave dell'elemento/)).toHaveText(el.key);
  // La ricerca nel catalogo accetta anche la chiave.
  await page.getByPlaceholder(/nome, codice o chiave/).fill(el.key);
  await expect(page.getByRole("row").filter({ hasText: "APP-MOB" })).toBeVisible();
});

test("mappa dell'architettura: etichette dei nodi visibili sopra una soglia di zoom e «Etichette sempre»", async ({ page }) => {
  await login(page, "arch");
  await page.goto("/architettura/mappa");
  const labels = page.locator("svg[role=img] text");
  await expect(labels.first()).toBeVisible();
  await expect.poll(() => labels.count()).toBeGreaterThan(3);
  // Molto lontano i nomi spariscono; con «Etichette sempre» tornano.
  for (let i = 0; i < 9; i++) await page.getByRole("button", { name: "Riduci" }).click();
  await expect.poll(() => labels.count()).toBe(0);
  await page.getByLabel("Etichette sempre").check();
  await expect.poll(() => labels.count()).toBeGreaterThan(3);
  // Il codice di catalogo compare nel nome dell'etichetta.
  await expect(labels.filter({ hasText: "APP-MOB" }).first()).toBeAttached();
});
