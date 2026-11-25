// Ondata 2, crisi e dal problema all'azione (replica/usabilita.md, proposte 3 e 5): la vista di
// crisi dell'incidente (telefono prima: barra d'azione fissa, bersagli da 44 px, orologio, bozza
// della notifica con i campi mancanti), e le azioni che partono da una vulnerabilità, un blocco del
// gate, un requisito e un'aderenza mancante, con il collegamento visibile da entrambe le parti.
import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, scegli, uniq } from "./fixtures";

const MOBILE = { width: 375, height: 812 };

type AppItem = { key: string; name?: string; title?: string };

async function firstApplication(req: APIRequestContext): Promise<{ key: string; name: string }> {
  const { items } = await call<{ items: AppItem[] }>(req, "GET", "/api/v1/architecture/applications");
  expect(items.length).toBeGreaterThan(0);
  return { key: items[0].key, name: items[0].name ?? items[0].title ?? "" };
}

/** Incidente grave già classificato (con le notifiche normative), sull'applicazione indicata. */
async function crisisIncident(req: APIRequestContext, title: string, impacted: string[], opts: { sparse?: boolean } = {}) {
  const inc = await call<{ key: string; element: { id: string } }>(req, "POST", "/api/v1/incidents", {
    title,
    description: "Creato dai test end-to-end.",
    detected_at: new Date(Date.now() - 2 * 3_600_000).toISOString(),
    // «sparse»: senza la causa, così nella bozza manca un campo obbligatorio.
    clients_affected: 180000,
    clients_affected_pct: 14,
    downtime_minutes: 190,
    economic_impact_cents: 21000000,
    ...(opts.sparse ? {} : { cause: "system" }),
    impacted,
  });
  await call(req, "POST", `/api/v1/elements/${inc.element.id}/transitions`, { to: "Classificato" });
  return { key: inc.key, id: inc.element.id };
}

test("vista di crisi a 375 px: orologio, azioni in barra fissa da 44 px, decisione e bozza della notifica", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "incidenti");
  const app = await firstApplication(req);
  const inc = await crisisIncident(req, uniq("Crisi telefono"), [app.key], { sparse: true });
  await page.setViewportSize(MOBILE);
  await login(page, "incidenti");
  await page.goto(`/crisi/${inc.key}`);
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Crisi telefono");

  // L'orologio della prossima notifica è subito in vista, con il conto alla rovescia.
  const clock = page.getByRole("region", { name: "Orologio delle notifiche" });
  await expect(clock).toBeInViewport();
  await expect(clock.getByText("Prossima notifica")).toBeVisible();
  await expect(clock.getByRole("timer").first()).toContainText(/mancano|scaduta/);

  // La barra d'azione è fissa in basso e i pulsanti hanno un bersaglio di almeno 44 px.
  const bar = page.getByRole("navigation", { name: "Azioni dell'incidente" });
  await expect(bar).toBeVisible();
  expect(await bar.evaluate((el) => getComputedStyle(el).position)).toBe("fixed");
  for (const b of await bar.getByRole("button").all()) {
    expect((await b.boundingBox())!.height, "altezza del bersaglio").toBeGreaterThanOrEqual(44);
  }
  await expectNoHorizontalScroll(page);
  // Resta in vista anche in fondo alla pagina.
  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  await expect(bar).toBeInViewport();
  const box = (await bar.boundingBox())!;
  expect(Math.round(box.y + box.height), "la barra sta sul bordo basso").toBeGreaterThanOrEqual(MOBILE.height - 2);

  // Una decisione si registra dalla barra e compare sotto «Decisioni e comunicazioni».
  await bar.getByRole("button", { name: "Registra" }).click();
  await page.getByRole("dialog").getByRole("button", { name: /^Decisione/ }).click();
  const text = uniq("Passiamo al sito di ripristino");
  await page.getByRole("dialog").getByLabel("Testo").fill(text);
  await page.getByRole("dialog").getByRole("button", { name: "Registra", exact: true }).click();
  const decisions = page.getByRole("region", { name: "Decisioni e comunicazioni" });
  await expect(decisions).toContainText(text);

  // La bozza ha i dati dell'incidente e segnala il campo obbligatorio che manca (il responsabile).
  const draft = page.getByRole("region", { name: "Bozza della notifica" });
  await draft.scrollIntoViewIfNeeded();
  await expect(draft.getByText("Clienti coinvolti", { exact: true })).toBeVisible();
  await expect(draft.locator('[data-missing="true"]').filter({ hasText: "Manca" }).first()).toContainText("Causa");
  await expect(draft).toContainText(/Manc(a|ano) \d+ camp/);

  // «Notifica» apre il testo precompilato dai dati; salvare la bozza la registra sulla notifica.
  await bar.getByRole("button", { name: "Notifica" }).click();
  const body = draft.getByLabel("Testo della notifica");
  await expect(body).toBeVisible();
  await expect(body).toHaveValue(/Incidente: INC-\d+/);
  await expect(body).toHaveValue(/Fermo: /);
  await draft.getByRole("button", { name: "Salva la bozza" }).click();
  await expect.poll(async () => {
    const v = await call<{ notifications: { status: string; content: string }[] }>(req, "GET", `/api/v1/incidents/${inc.key}`);
    return v.notifications.some((n) => n.status === "draft" && n.content.includes("Fermo: "));
  }).toBe(true);

  // La cronologia ha le scadenze future come segnaposto.
  await expect(page.locator('[data-future="true"]').first()).toBeVisible();
  await expectNoHorizontalScroll(page);
});

test("vista di crisi sul desktop: due colonne, barra in riga, applicazioni colpite", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "incidenti");
  const app = await firstApplication(req);
  const inc = await crisisIncident(req, uniq("Crisi desktop"), [app.key]);
  await login(page, "incidenti");
  await page.goto(`/crisi/${inc.key}`);
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Crisi desktop");
  const bar = page.getByRole("navigation", { name: "Azioni dell'incidente" });
  expect(await bar.evaluate((el) => getComputedStyle(el).position)).toBe("static");
  const impact = page.getByRole("region", { name: "Impatto su applicazioni e servizi" });
  await expect(impact.getByRole("link", { name: new RegExp(app.key) }).first()).toBeVisible();
  await expect(impact).toContainText("Ultimo rilascio");
  await expect(impact).toContainText("Vulnerabilità");
  await expect(page.getByRole("region", { name: "Cronologia" })).toBeVisible();
  // Impatto a sinistra, decisioni e bozza a destra.
  const left = (await impact.boundingBox())!;
  const right = (await page.getByRole("region", { name: "Decisioni e comunicazioni" }).boundingBox())!;
  expect(right.x, "colonna di destra").toBeGreaterThan(left.x + left.width - 4);
});

test("la banda dell'incidente porta alla vista di crisi", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "incidenti");
  const inc = await crisisIncident(req, uniq("Banda crisi"), []);
  await login(page, "incidenti");
  await page.goto(`/e/${inc.key}`);
  await page.getByLabel("Da fare sull'incidente").getByRole("link", { name: "Apri la vista di crisi" }).click();
  await expect(page).toHaveURL(new RegExp(`/crisi/${inc.key}$`));
  await expect(page.getByRole("region", { name: "Orologio delle notifiche" })).toBeVisible();
  // Senza applicazioni collegate la vista lo dice, e lo dice anche la bozza.
  await expect(page.getByRole("region", { name: "Impatto su applicazioni e servizi" })).toContainText("Nessuna applicazione collegata");
});

test("incidente chiuso: la prossima azione è il postmortem", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "incidenti");
  const inc = await crisisIncident(req, uniq("Crisi chiusa"), []);
  await call(req, "PATCH", `/api/v1/incidents/${inc.key}`, { resolved_at: new Date(Date.now() - 3_600_000).toISOString() });
  const v = await call<{ next: { kind: string } }>(req, "GET", `/api/v1/incidents/${inc.key}/crisis`);
  expect(["close", "notify", "postmortem"]).toContain(v.next.kind);
  await login(page, "incidenti");
  await page.goto(`/crisi/${inc.key}`);
  await expect(page.getByRole("region", { name: "Prossima azione" })).toBeVisible();
});

// --------------------------------------------------------------------------------------------
// Dal problema all'azione

async function pickTeam(page: Page, dialog: ReturnType<Page["getByRole"]>) {
  // Se l'applicazione non ha un team titolare, il team si sceglie a mano.
  const create = dialog.getByRole("button", { name: "Crea", exact: true });
  if (await create.isDisabled()) await scegli(dialog.getByRole("combobox", { name: "Team" }), { index: 1 });
  await expect(create).toBeEnabled();
}

test("vulnerabilità: crea l'elemento di lavoro e chiedi l'eccezione, con il collegamento da entrambe le parti", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const { items } = await call<{ items: { id: string; cve: string; application: string | null; application_key: string | null; exception: string | null; stage: string }[] }>(
    req, "GET", "/api/v1/devsecops/vuln/items?stage=open&limit=100");
  const it = items.find((x) => x.stage === "open" && !x.exception && x.application_key && x.application);
  test.skip(!it, "nessuna vulnerabilità aperta con un'applicazione nei dati demo");
  await login(page, "admin");
  await page.goto(`/devsecops/vulnerabilita?q=${encodeURIComponent(it!.cve)}`);
  const row = page.locator("li").filter({ has: page.getByRole("button", { name: new RegExp(it!.cve) }) }).filter({ hasText: it!.application! }).first();
  await row.getByRole("button", { name: new RegExp(it!.cve) }).click();
  const region = row.getByRole("region", { name: "Lavoro e azioni collegate" });
  await expect(region).toContainText("Nessuna azione ancora");

  // Elemento di lavoro: titolo e descrizione arrivano dal problema.
  await region.getByRole("button", { name: "Crea elemento di lavoro" }).click();
  const dialog = page.getByRole("dialog", { name: "Crea elemento di lavoro" });
  await expect(dialog.getByLabel("Titolo")).toHaveValue(new RegExp(it!.cve));
  await expect(dialog.getByLabel("Descrizione")).toHaveValue(/Origine: vulnerabilità/);
  await pickTeam(page, dialog);
  await dialog.getByRole("button", { name: "Crea", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  const workLink = region.getByRole("link", { name: /^[A-Z]+-\d+/ }).first();
  await expect(workLink).toBeVisible();
  await expect(region).toContainText("Lavoro");
  const taskKey = ((await workLink.innerText()).match(/[A-Z]+-\d+/) ?? [""])[0];
  expect(taskKey).not.toBe("");

  // Eccezione: il motivo è già avviato dal problema.
  await region.getByRole("button", { name: "Chiedi eccezione" }).click();
  const exc = page.getByRole("dialog", { name: "Chiedi eccezione" });
  const reason = exc.getByLabel("Motivazione");
  await expect(reason).toHaveValue(new RegExp(`Non si corregge subito ${it!.cve}`));
  const send = exc.getByRole("button", { name: "Chiedi l'approvazione" });
  await expect(send, "il motivo da solo non basta: va completato").toBeDisabled();
  await reason.fill(`${await reason.inputValue()}il componente non è raggiungibile dall'esterno.`);
  await send.click();
  await expect(exc).toHaveCount(0);
  await expect(region.getByText("Eccezione", { exact: true })).toBeVisible();
  await expect(region).toContainText(/EV-\d+/);

  // Dall'altra parte: l'elemento di lavoro è collegato all'applicazione e dice da quale problema nasce.
  await page.goto(`/e/${taskKey}`);
  await expect(page.getByText(it!.application!).first()).toBeVisible();
  const origins = await call<{ items: { kind: string; ref: string; application: { key: string } | null }[] }>(req, "GET", `/api/v1/elements/${taskKey}/problems`);
  expect(origins.items).toHaveLength(1);
  expect(origins.items[0].kind).toBe("finding");
  expect(origins.items[0].application?.key).toBe(it!.application_key);
  const links = await call<{ relations: { key: string; relation: string }[] }>(req, "GET", `/api/v1/elements/${taskKey}/links`);
  expect(links.relations.some((r) => r.key === it!.application_key)).toBe(true);
});

test("gate: lavoro dal blocco e override tracciato accanto al motivo", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const { items } = await call<{ items: { id: string; result: string; can_override: boolean; override?: unknown; application: { key: string } }[] }>(
    req, "GET", "/api/v1/devsecops/gate/decisions?result=fail&limit=50");
  const d = items.find((x) => x.result === "fail" && x.can_override && !x.override);
  test.skip(!d, "nessun blocco del gate nei dati demo");
  // L'override chiesto dal modulo del gate compare tra i collegamenti del problema, senza altri passaggi.
  const ctxBefore = await call<{ links: { action: string }[]; reason: string; can: { work_item: boolean } }>(req, "GET", `/api/v1/problems/context?kind=gate_decision&ref=${d!.id}`);
  expect(ctxBefore.reason).toContain("Si rilascia lo stesso nonostante");
  await call(req, "POST", "/api/v1/devsecops/gate/overrides", { decision: d!.id, reason: "Urgenza dei test end-to-end: rilascio concordato.", hours: 2 });
  const ctxAfter = await call<{ links: { action: string; status: string }[] }>(req, "GET", `/api/v1/problems/context?kind=gate_decision&ref=${d!.id}`);
  expect(ctxAfter.links.some((l) => l.action === "override" && l.status === "pending")).toBe(true);

  // Dalla pagina dell'applicazione: «Motivi» mostra le azioni sul blocco e il lavoro si crea da lì.
  await login(page, "admin");
  await page.goto(`/e/${d!.application.key}?scheda=gate`);
  const buttons = page.getByRole("button", { name: "Motivi" });
  await expect(buttons.first()).toBeVisible();
  for (const b of await buttons.all()) await b.click();
  const region = page.getByRole("region", { name: "Lavoro e azioni collegate" }).first();
  await expect(region).toBeVisible();
  await region.getByRole("button", { name: "Crea elemento di lavoro" }).click();
  const dialog = page.getByRole("dialog", { name: "Crea elemento di lavoro" });
  await expect(dialog.getByLabel("Titolo")).toHaveValue(/Sbloccare il rilascio di/);
  await pickTeam(page, dialog);
  await dialog.getByRole("button", { name: "Crea", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(region.getByRole("link", { name: /^[A-Z]+-\d+/ }).first()).toBeVisible();
});

test("requisito: crea l'elemento di lavoro dal requisito non soddisfatto", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const found = await call<{ items: { key: string }[] }>(req, "GET", `/api/v1/elements?q=${encodeURIComponent("tipo: requirement")}&limit=60`);
  let key = "";
  for (const e of found.items) {
    const v = await call<{ cells: { status: string }[] }>(req, "GET", `/api/v1/requirements/${e.key}`);
    if (v.cells.some((c) => !["verified", "waived", "not_applicable"].includes(c.status))) {
      key = e.key;
      break;
    }
  }
  test.skip(!key, "nessun requisito non soddisfatto nei dati demo");
  await login(page, "admin");
  await page.goto(`/e/${key}`);
  await page.getByRole("tab", { name: "Verifica" }).click();
  const region = page.getByRole("region", { name: "Lavoro e azioni collegate" });
  await expect(region).toBeVisible();
  await region.getByRole("button", { name: "Crea elemento di lavoro" }).click();
  const dialog = page.getByRole("dialog", { name: "Crea elemento di lavoro" });
  await expect(dialog.getByLabel("Titolo")).toHaveValue(new RegExp(`Soddisfare il requisito ${key}`));
  await pickTeam(page, dialog);
  await dialog.getByRole("button", { name: "Crea", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  const link = region.getByRole("link", { name: /^[A-Z]+-\d+/ }).first();
  await expect(link).toBeVisible();
  const taskKey = ((await link.innerText()).match(/[A-Z]+-\d+/) ?? [""])[0];
  // Dall'altra parte: il lavoro è collegato al requisito nel grafo.
  const links = await call<{ relations: { key: string; relation: string }[] }>(req, "GET", `/api/v1/elements/${taskKey}/links`);
  expect(links.relations.some((r) => r.key === key)).toBe(true);
  // E il requisito vede il lavoro tra i suoi collegamenti.
  const back = await call<{ relations: { key: string }[] }>(req, "GET", `/api/v1/elements/${key}/links`);
  expect(back.relations.some((r) => r.key === taskKey)).toBe(true);
});

test("aderenza mancante: lavoro e pull request di adozione collegati alla stessa applicazione", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const ad = await call<{ configured: boolean; applications: { id: string; key: string; name: string; status: string; missing: string[] }[] }>(req, "GET", "/api/v1/devsecops/adherence");
  const app = ad.applications.find((a) => a.status !== "aderente");
  test.skip(!ad.configured || !app, "nessuna aderenza mancante nei dati demo");
  const pr = `https://git.example.test/org/${uniq("repo").replace(/\s+/g, "-")}/pull/7`;
  await call(req, "POST", "/api/v1/problems/links", { kind: "adherence", ref: app!.key, action: "adoption_pr", target_ref: pr, target_label: "Adozione della pipeline" });
  await login(page, "admin");
  await page.goto("/devsecops/aderenza");
  const row = page.locator("li").filter({ has: page.getByRole("button", { name: new RegExp(app!.name) }) }).first();
  await row.getByRole("button", { name: new RegExp(app!.name) }).first().click();
  const region = row.getByRole("region", { name: "Lavoro e azioni collegate" });
  await expect(region.getByRole("link", { name: "Adozione della pipeline" })).toHaveAttribute("href", pr);
  await region.getByRole("button", { name: "Crea elemento di lavoro" }).click();
  const dialog = page.getByRole("dialog", { name: "Crea elemento di lavoro" });
  await expect(dialog.getByLabel("Titolo")).toHaveValue(/Portare la pipeline di .* alla politica/);
  await pickTeam(page, dialog);
  await dialog.getByRole("button", { name: "Crea", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(region.getByRole("link", { name: /^[A-Z]+-\d+/ }).first()).toBeVisible();
  await expect(region).toContainText("Pull request");
});

test.describe("in inglese", () => {
  test.use({ locale: "en-US" });

  test("vista di crisi e azioni dal problema in inglese", async ({ page, playwright, baseURL }) => {
    const { req } = await apiAs(playwright, baseURL!, "incidenti");
    const app = await firstApplication(req);
    const inc = await crisisIncident(req, uniq("Crisis in English"), [app.key], { sparse: true });
    await page.setViewportSize(MOBILE);
    await login(page, "incidenti");
    await page.goto(`/crisi/${inc.key}`);
    await expect(page.getByRole("region", { name: "Notification clock" })).toBeVisible();
    await expect(page.getByText("Next notification")).toBeVisible();
    await expect(page.getByRole("region", { name: "Next action" })).toBeVisible();
    const bar = page.getByRole("navigation", { name: "Incident actions" });
    await expect(bar.getByRole("button", { name: "Record" })).toBeVisible();
    await expect(page.getByRole("region", { name: "Impact on applications and services" })).toBeVisible();
    await expect(page.getByRole("region", { name: "Providers involved" })).toBeVisible();
    await expect(page.getByRole("region", { name: "Decisions and communications" })).toBeVisible();
    const draft = page.getByRole("region", { name: "Notification draft" });
    await expect(draft).toContainText("Clients affected");
    await expect(draft).toContainText(/required fields? (is|are) missing/);
    await expect(page.getByRole("region", { name: "Timeline" })).toBeVisible();
    const main = (await page.locator("main").innerText()).replace(/\s+/g, " ");
    for (const w of ["Prossima", "Decisioni", "Bozza", "Cronologia", "Fornitori", "Impatto", "Registra", "Manca"]) expect(main, `parola italiana «${w}»`).not.toContain(w);
    await expectNoHorizontalScroll(page);
    await bar.getByRole("button", { name: "Record" }).click();
    await expect(page.getByRole("dialog", { name: "What do you want to record?" })).toBeVisible();
    await page.getByRole("dialog").getByRole("button", { name: /^Decision/ }).click();
    await expect(page.getByRole("dialog", { name: "Decision" })).toBeVisible();
  });
});
