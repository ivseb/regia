// Ondata 1 dell'area lavoro: il link dalla coda delle approvazioni apre la scheda giusta con la riga
// «Aspetta la tua decisione» e i pulsanti in vista; fasi con nome e passo avanti in primo piano; attività
// sui commenti; etichette dello sprint e banner «sola lettura»; board con «Solo i miei» e sprint ricordato;
// contatore con etichetta nel tempo; testo del documento subito sotto il titolo; origine del budget della
// graduatoria; impostazione «si può decidere in linea» dell'organo. Con un caso a 375 px.
import { test, expect } from "@playwright/test";
import { advance, apiAs, call, expectNoHorizontalScroll, login, newRequest, uniq, scegli } from "./fixtures";

/** Richiesta portata alla fase di approvazione: lo sponsor (sotto 50 k€ basta lui) ha una decisione in attesa. */
async function requestInApproval(playwright: Parameters<typeof apiAs>[0], baseURL: string, label: string) {
  const { req } = await apiAs(playwright, baseURL, "richiedente");
  const title = uniq(label);
  const { id, key } = await newRequest(req, title);
  await req.dispose();
  const dm = await apiAs(playwright, baseURL, "dm");
  await advance(dm.req, id, "Triage", "Valutazione");
  await call(dm.req, "PATCH", `/api/v1/demand/requests/${id}`, { estimate_low_cents: 2_000_000, estimate_high_cents: 3_000_000 });
  await advance(dm.req, id, "Prioritizzazione");
  const el = await call(dm.req, "GET", `/api/v1/elements/${id}`);
  await call(dm.req, "PATCH", `/api/v1/elements/${id}`, { version: el.element.version, fields: { data_golive: "2027-05-31" } });
  await advance(dm.req, id, "Approvazione");
  await dm.req.dispose();
  return { id, key, title };
}

test("approvazioni: il link apre la scheda giusta, con la decisione in vista", async ({ page, playwright, baseURL }) => {
  const { key, title } = await requestInApproval(playwright, baseURL!, "Archivio digitale delle delibere");
  await login(page, "sponsor");
  await page.goto("/approvazioni");
  await page.getByRole("link", { name: title }).click();
  await expect(page).toHaveURL(new RegExp(`/e/${key}\\?scheda=approvazioni`));
  // La riga in testata, la scheda «Approvazioni» aperta e il pulsante già in vista.
  await expect(page.getByRole("status").filter({ hasText: "Aspetta la tua decisione" })).toBeVisible();
  await expect(page.getByRole("tab", { name: /Approvazioni/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("button", { name: "Approva", exact: true })).toBeInViewport();
  // Senza `scheda` nell'indirizzo, chi deve decidere atterra comunque sulle approvazioni.
  await page.goto(`/e/${key}`);
  await expect(page.getByRole("tab", { name: /Approvazioni/ })).toHaveAttribute("aria-selected", "true");
  // Le fasi hanno il nome sotto il segmento.
  const fasi = page.getByRole("group", { name: /^Fase: / });
  await expect(fasi).toContainText("Prioritizzazione");
  await expect(fasi).toContainText("Approvazione");
});

test.describe("approvazione dal telefono (375 px)", () => {
  test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

  test("la decisione si raggiunge e i bersagli sono da 44 px", async ({ page, playwright, baseURL }) => {
    const { key, title } = await requestInApproval(playwright, baseURL!, "Registro dei trattamenti fornitori");
    await login(page, "sponsor");
    await page.goto("/approvazioni");
    await expectNoHorizontalScroll(page);
    const link = page.getByRole("link", { name: title });
    expect((await link.boundingBox())!.height).toBeGreaterThanOrEqual(43.5);
    await link.click();
    await expect(page).toHaveURL(new RegExp(`/e/${key}\\?scheda=approvazioni`));
    await expect(page.getByRole("status").filter({ hasText: "Aspetta la tua decisione" })).toBeVisible();
    const approva = page.getByRole("button", { name: "Approva", exact: true });
    await expect(approva).toBeInViewport();
    for (const name of ["Approva", "Con condizioni", "Rinvia", "Respingi"]) {
      const box = await page.getByRole("button", { name, exact: true }).boundingBox();
      expect(box!.height, name).toBeGreaterThanOrEqual(43.5);
    }
    // La fase si legge anche senza spazio per i nomi: «Fase N di M».
    await expect(page.getByRole("group", { name: /^Fase: / })).toContainText(/Fase \d+ di \d+/);
    await expectNoHorizontalScroll(page);
    await approva.click();
    await expect(page.getByText("Fase attuale: Pianificazione")).toBeVisible();
  });
});

test("dettaglio: passo avanti in primo piano, attività sui commenti con gli eventi ripiegati", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const { id, key } = await newRequest(req, uniq("Cruscotto per il collegio sindacale"));
  await req.dispose();
  const dm = await apiAs(playwright, baseURL!, "dm");
  await advance(dm.req, id, "Triage");
  await call(dm.req, "POST", `/api/v1/elements/${id}/comments`, { body: "Primo commento di prova." });
  await dm.req.dispose();
  await login(page, "dm");
  await page.goto(`/e/${key}`);
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  // Il passo in avanti è il pulsante principale; «Rimanda» resta ma è discreto.
  await expect(page.getByRole("button", { name: "Avvia la valutazione" })).toHaveClass(/btn-primary/);
  await expect(page.getByRole("button", { name: /Rimanda al richiedente/ })).toHaveClass(/btn-quiet/);
  // Attività (la richiesta si apre sulla «Sintesi»): i commenti per primi, gli eventi di sistema dietro un'etichetta con il numero.
  await page.getByRole("tab", { name: "Attività" }).click();
  await expect(page.getByRole("radio", { name: /^Commenti/ })).toHaveAttribute("aria-checked", "true");
  await expect(page.getByText("Primo commento di prova.")).toBeVisible();
  const eventi = page.getByRole("button", { name: /eventi? di sistema · Mostra/ });
  await expect(eventi).toBeVisible();
  await eventi.click();
  await expect(page.getByRole("radio", { name: "Tutto" })).toHaveAttribute("aria-checked", "true");
});

test("backlog e board: etichette dello sprint, «Solo i miei», sprint ricordato, sola lettura", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const team = uniq("Team ondata1");
  await call(req, "POST", "/api/v1/teams", { name: team });
  const spaces = await call(req, "GET", "/api/v1/work/spaces");
  const space = spaces.items.find((s: { team: string }) => s.team === team).key as string;
  const sprint = await call(req, "POST", `/api/v1/work/spaces/${space}/sprints`, { title: "Sprint uno", goal: "Provare le etichette" });
  await call(req, "POST", `/api/v1/work/spaces/${space}/items`, { type: "story", title: "Voce nello sprint", fields: { punti: 5 }, sprint: sprint.key });
  await req.dispose();

  await login(page, "admin");
  await page.goto(`/delivery/backlog?spazio=${space}`);
  await expect(page.getByRole("heading", { name: "Backlog e sprint" })).toBeVisible();
  await expect(page.getByText(/5 pt impegnati · 0 fatti · 0 di 1 chiusi/)).toBeVisible();

  // «Solo i miei» scrive il filtro nella query e si toglie con un secondo clic.
  const mine = page.getByRole("button", { name: "Solo i miei" });
  await mine.click();
  await expect(page).toHaveURL(/q=assegnatario/);
  await expect(mine).toHaveAttribute("aria-pressed", "true");
  await mine.click();
  await expect(page).not.toHaveURL(/q=assegnatario/);

  // La board ricorda lo sprint scelto per quel team.
  await page.goto(`/delivery/board?spazio=${space}`);
  const scelta = page.getByRole("combobox", { name: /^Sprint/ });
  await expect(scelta).toBeVisible();
  await scegli(scelta, { value: sprint.key });
  await expect(page.getByText("Voce nello sprint")).toBeVisible();
  await page.goto(`/delivery/board?spazio=${space}`);
  await expect(page.getByRole("combobox", { name: /^Sprint/ })).toHaveAttribute("data-value", sprint.key);
  await expect(page.getByRole("button", { name: "Solo i miei" })).toBeVisible();

  // Chi non può modificare il lavoro lo sa: banner «Sola lettura».
  await page.context().clearCookies();
  await login(page, "sponsor");
  await page.goto(`/delivery/backlog?spazio=${space}`);
  await expect(page.getByRole("heading", { name: "Backlog e sprint" })).toBeVisible();
  await expect(page.getByRole("note").filter({ hasText: "Sola lettura" })).toBeVisible();
});

test("tempo: una sola navigazione tra Timesheet, Report e Team, con il contatore etichettato", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  await call(req, "PUT", "/api/v1/work/time/settings", { require_approval: true, approver_mode: "admin" });
  const team = uniq("Team tempo");
  await call(req, "POST", "/api/v1/teams", { name: team });
  const spaces = await call(req, "GET", "/api/v1/work/spaces");
  const space = spaces.items.find((s: { team: string }) => s.team === team).key as string;
  const item = await call(req, "POST", `/api/v1/work/spaces/${space}/items`, { type: "story", title: "Lavoro da registrare" });
  const d = new Date();
  d.setDate(d.getDate() - ((d.getDay() + 6) % 7)); // lunedì della settimana corrente
  const week = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  await call(req, "PUT", "/api/v1/work/timesheet/cells", { week, cells: [{ element: item.key, date: week, duration: "2h" }] });
  await call(req, "POST", "/api/v1/work/timesheet/submit", { week });
  const pending = await call(req, "GET", "/api/v1/work/timesheet/pending");
  await req.dispose();

  await login(page, "admin");
  await page.goto("/tempo/report");
  const nav = page.getByRole("navigation", { name: "Tempo" });
  await expect(nav.getByRole("link")).toHaveCount(3);
  // Chi approva vede quanti timesheet aspettano, con l'etichetta.
  await page.goto("/tempo");
  expect(pending.items.length).toBeGreaterThan(0);
  await expect(nav.getByRole("link", { name: /Timesheet/ })).toContainText(/\d+ da approvare/);
});

test("documento controllato: il testo segue il titolo, anche sul telefono", async ({ page, playwright, baseURL }) => {
  const { req, me } = await apiAs(playwright, baseURL!, "admin");
  const memberId = me.active_tenant!.member_id;
  const space = "O" + Math.random().toString(36).slice(2, 6).toUpperCase();
  await call(req, "POST", "/api/v1/kb/spaces", { key: space, name: uniq("Spazio ondata1") });
  const body = "## Scopo\n\nQuesta policy definisce come si trattano i dati.\n\n## Ambito\n\nTutte le applicazioni web.";
  const doc = await call(req, "POST", `/api/v1/kb/spaces/${space}/pages`, { title: "Policy di sicurezza delle applicazioni web e dei servizi collegati", body });
  await call(req, "PUT", `/api/v1/kb/pages/${doc.page.key}/control`, { controlled: true, review_months: 12, reviewers: [memberId], approvers: [memberId] });
  await req.dispose();

  await login(page, "admin");
  await page.setViewportSize({ width: 1024, height: 768 });
  await page.goto(`/documenti/${doc.page.key}`);
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  const h1 = await page.getByRole("heading", { level: 1 }).boundingBox();
  const colonna = await page.getByRole("article").boundingBox();
  expect(h1!.width, "il titolo ha tutta la riga, non lo spazio lasciato dai pulsanti").toBeGreaterThan(colonna!.width * 0.6);
  const scopo = await page.getByRole("heading", { name: "Scopo" }).boundingBox();
  expect(scopo!.y, "il primo titolo del testo sta nella prima schermata (prima era a 987 px)").toBeLessThan(600);
  await expect(page.getByRole("region", { name: "Documento controllato" })).toContainText("In vigore");

  await page.setViewportSize({ width: 375, height: 812 });
  await page.reload();
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  const scopoTel = await page.getByRole("heading", { name: "Scopo" }).boundingBox();
  expect(scopoTel!.y, "anche sul telefono entro la prima schermata e mezza").toBeLessThan(1000);
  await expectNoHorizontalScroll(page);
});

test("graduatoria: dice da dove viene il budget", async ({ page }) => {
  await login(page, "pmo");
  await page.goto("/graduatoria");
  await expect(page.getByRole("heading", { name: "Graduatoria del portafoglio" })).toBeVisible();
  const nota = page.locator("[data-budget-source]");
  await expect(nota).toBeVisible();
  await expect(nota).toContainText(/budget/i);
});

test("organo di governo: «si può decidere in linea» si imposta e arriva nell'API", async ({ page, playwright, baseURL }) => {
  const { req, me } = await apiAs(playwright, baseURL!, "admin");
  // Un organo proprio del test: non si tocca il comitato condiviso (lo usano anche gli altri test, in parallelo).
  const created = await call(req, "POST", "/api/v1/governance/bodies", { name: uniq("Tavolo di prova"), kind: "steering", chair_id: me.active_tenant!.member_id });
  expect(created.inline_decision).toBe(true);
  const before = await call(req, "GET", "/api/v1/governance/bodies");
  const body = before.items.find((b: { id: string }) => b.id === created.id);
  expect(body.inline_decision).toBe(true);

  await login(page, "admin");
  await page.goto("/governance");
  await expect(page.getByRole("heading", { name: /Governance/ })).toBeVisible();
  const card = page.getByRole("listitem").filter({ hasText: body.name }).first();
  await card.getByRole("button", { name: /Modifica/ }).click();
  const check = page.getByRole("checkbox", { name: /Si può decidere in linea/ });
  await expect(check).toBeChecked();
  await check.uncheck();
  await page.getByRole("button", { name: /^Salva/ }).click();
  await expect(card.getByText("dopo l'apertura della richiesta").first()).toBeVisible();
  const after = await call(req, "GET", "/api/v1/governance/bodies");
  expect(after.items.find((b: { id: string }) => b.id === body.id).inline_decision).toBe(false);

  // Si ripristina il valore predefinito per gli altri test.
  await card.getByRole("button", { name: /Modifica/ }).click();
  await page.getByRole("checkbox", { name: /Si può decidere in linea/ }).check();
  await page.getByRole("button", { name: /^Salva/ }).click();
  await expect(card.getByText("dopo l'apertura della richiesta")).toHaveCount(0);
  await req.dispose();
});
