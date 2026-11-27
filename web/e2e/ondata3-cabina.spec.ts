// Ondata 3, proposta 9 «Cabina di portafoglio»: si sposta la linea di taglio e si vedono residuo e team in
// sovraccarico, si salvano e si confrontano due scenari, si genera il verbale della seduta, a 375 px la
// roadmap è una lista per mese.
import { test, expect } from "@playwright/test";
import { advance, apiAs, call, expectNoHorizontalScroll, login, newRequest, scegli, uniq } from "./fixtures";

test("cabina: linea di taglio, sovraccarico, scenari, verbale e telefono", async ({ page, playwright, baseURL }) => {
  const { req: rq } = await apiAs(playwright, baseURL!, "richiedente");
  const a = await newRequest(rq, uniq("Cabina prima richiesta"));
  const b = await newRequest(rq, uniq("Cabina seconda richiesta"));
  await rq.dispose();

  // Un team proprio con un solo giorno di capacità a settimana: due richieste da 20 giorni lo sovraccaricano.
  const { req: admin, me } = await apiAs(playwright, baseURL!, "admin");
  const teamName = uniq("Team Cabina");
  const team = await call<{ id: string }>(admin, "POST", "/api/v1/teams", { name: teamName, capacity_days_week: 1 });
  const { req: dm } = await apiAs(playwright, baseURL!, "dm");
  const app = (await call(dm, "GET", `/api/v1/architecture/applications?q=IAM`)).items[0].id as string;
  const app2 = (await call(dm, "GET", `/api/v1/architecture/applications?q=API-CLI`)).items[0].id as string;
  for (const [r, ap] of [[a, app], [b, app2]] as const) {
    await advance(dm, r.id, "Triage", "Valutazione");
    await call(dm, "POST", `/api/v1/demand/requests/${r.id}/impacts`, { application_id: ap, team_id: team.id, change_kind: "change", estimate_effort_days: 20 });
    await call(dm, "POST", `/api/v1/demand/requests/${r.id}/plan/auto`, { replan: true });
  }
  await dm.dispose();

  // Una sola chiamata aggrega tutto: graduatoria, plafond, capacità, finestre e scenari.
  const cab = await call(admin, "GET", "/api/v1/portfolio/cabina");
  expect(cab.ranking.items.length).toBeGreaterThan(1);
  expect(cab.teams.some((t: { name: string }) => t.name === teamName)).toBeTruthy();
  expect(cab.rows[cab.ranking.items[0].id]).toHaveProperty("efforts");

  await login(page, "admin");
  await page.goto("/cabina");
  await expect(page.getByRole("heading", { level: 1, name: "Cabina di portafoglio" })).toBeVisible();
  const budgetBox = page.getByRole("textbox", { name: "Budget disponibile" });
  await expect(budgetBox).toBeVisible();
  if (cab.plafond) await expect(page.locator("[data-plafond]")).toBeVisible();

  // Budget: «aggiungi k€» sposta il valore di 100.
  const kEur = async () => Number((await budgetBox.inputValue()).replace(/\./g, "").replace(",", ".") || "0");
  const before = await kEur();
  await page.getByRole("button", { name: "Aggiungi 100 k€" }).click();
  await expect.poll(kEur).toBe(before + 100);
  await page.getByRole("button", { name: "Togli 10 k€" }).click();
  await expect.poll(kEur).toBe(before + 90);

  // Linea di taglio in fondo (tutto dentro): il team è in sovraccarico; riga e pannello lo dicono.
  const slider = page.getByRole("slider", { name: "Linea di taglio" });
  await slider.focus();
  await page.keyboard.press("End");
  const total = Number(await slider.getAttribute("aria-valuemax"));
  await expect(slider).toHaveAttribute("aria-valuenow", String(total));
  const stats = page.getByRole("region", { name: "Budget disponibile" });
  await expect(stats.getByRole("definition").nth(0)).toHaveText(new RegExp(`^${total} di ${total}$`));
  const capacity = page.getByRole("region", { name: "Capacità dei team" });
  await expect(capacity.getByRole("button", { name: new RegExp(teamName) })).toContainText(/\d+%/);
  await expect(capacity.getByText(/settimane? oltre la capacità/).first()).toBeVisible();
  await expect(stats.getByRole("definition").nth(3)).not.toHaveText("nessuno");

  // L'icona di sovraccarico della riga apre «Chi occupa …» con la proposta di spostamento.
  const row = page.getByRole("row").filter({ hasText: a.key }).first();
  await row.getByRole("button", { name: new RegExp(`^Sovraccarico: ${teamName}`) }).click();
  const who = page.getByRole("region", { name: `Chi occupa ${teamName}` });
  await expect(who).toBeVisible();
  await expect(who.getByText(a.key)).toBeVisible();
  await expect(who.locator("[data-suggestion]")).toContainText(/^Sposta DEM-\d+ (dopo DEM-\d+|in fondo)/);
  // Il motivo della posizione è scritto in ogni riga.
  await expect(page.locator("[data-why]").first()).not.toBeEmpty();

  // Linea in cima: nessuna richiesta dentro, il team rientra nella capacità.
  await slider.focus();
  await page.keyboard.press("Home");
  await expect(slider).toHaveAttribute("aria-valuenow", "0");
  await expect(stats.getByRole("definition").nth(0)).toHaveText(new RegExp(`^0 di ${total}$`));
  await expect(capacity.getByRole("button", { name: new RegExp(teamName) })).toContainText("dentro la capacità");

  // Scenari: due linee con nome, confronto affiancato.
  const nameA = uniq("Scenario tutto");
  const nameB = uniq("Scenario niente");
  await page.getByRole("textbox", { name: "Nome dello scenario" }).fill(nameB);
  await page.getByRole("button", { name: /^Salva lo scenario/ }).click();
  await expect(page.getByRole("listitem").filter({ hasText: nameB })).toBeVisible();
  await slider.focus();
  await page.keyboard.press("End");
  await page.getByRole("textbox", { name: "Nome dello scenario" }).fill(nameA);
  await page.getByRole("button", { name: /^Salva lo scenario/ }).click();
  await expect(page.getByRole("listitem").filter({ hasText: nameA })).toBeVisible();
  await scegli(page.getByRole("combobox", { name: "Scenario A" }), nameA);
  await scegli(page.getByRole("combobox", { name: "Scenario B" }), nameB);
  const cmp = page.locator("[data-cabina-compare]");
  await expect(cmp.getByRole("columnheader", { name: nameA })).toBeVisible();
  await expect(cmp.getByRole("columnheader", { name: nameB })).toBeVisible();
  const overRow = cmp.getByRole("row").filter({ hasText: "Team in sovraccarico" });
  await expect(overRow.getByRole("cell").nth(0)).not.toHaveText("nessuno");
  await expect(overRow.getByRole("cell").nth(1)).toHaveText("nessuno");
  const inside = cmp.getByRole("row").filter({ hasText: "Richieste dentro" });
  await expect(inside.getByRole("cell").nth(0)).toHaveText(`${total} di ${total}`);
  await expect(inside.getByRole("cell").nth(1)).toHaveText(`0 di ${total}`);
  // Gli scenari sono dei dati del tenant: li legge anche l'API.
  const again = await call(admin, "GET", "/api/v1/portfolio/cabina");
  expect(again.scenarios.map((s: { name: string }) => s.name)).toEqual(expect.arrayContaining([nameA, nameB]));
  for (const s of again.scenarios.filter((x: { name: string }) => [nameA, nameB].includes(x.name))) await call(admin, "DELETE", `/api/v1/portfolio/scenarios/${s.id}`);

  // Pacchetto della seduta e verbale: una seduta con un punto deciso (presa d'atto, senza quorum).
  const body = await call<{ id: string }>(admin, "POST", "/api/v1/governance/bodies", { name: uniq("Tavolo cabina"), kind: "steering", chair_id: me.active_tenant!.member_id });
  const title = uniq("Seduta cabina");
  const meeting = await call<{ id: string }>(admin, "POST", "/api/v1/governance/meetings", {
    body_id: body.id, title, scheduled_at: new Date(Date.now() + 7 * 86_400_000).toISOString(), propose: false,
  });
  await call(admin, "POST", `/api/v1/governance/meetings/${meeting.id}/items`, { element: a.key });
  await call(admin, "POST", `/api/v1/governance/meetings/${meeting.id}/status`, { status: "held" });
  await call(admin, "POST", `/api/v1/governance/meetings/${meeting.id}/items/${a.key}/decision`, { decision: "informational", note: "Presa d'atto in cabina." });
  const pack = await call(admin, "GET", `/api/v1/portfolio/sedute/${meeting.id}/pacchetto`);
  expect(pack.items).toHaveLength(1);
  expect(pack.items[0]).toMatchObject({ key: a.key, outcome: "informational" });
  expect(pack.items[0].position).toBeGreaterThan(0);

  await page.reload();
  const pk = page.locator("[data-cabina-pack]");
  await scegli(pk.getByRole("combobox", { name: "Seduta" }), title);
  const agenda = pk.locator("[data-cabina-agenda]");
  await expect(agenda.getByRole("row").filter({ hasText: a.key })).toContainText("presa d'atto");
  await pk.getByRole("button", { name: "Genera verbale" }).click();
  const done = pk.locator("[data-verbale]");
  await expect(done).toContainText(/Verbale creato in Knowledge/);
  const key = (await done.getByRole("link").innerText()).trim();
  const kb = await call(admin, "GET", `/api/v1/kb/pages/${key}`);
  expect(kb.page.title).toContain(title);
  expect(kb.page.body).toContain(a.key);
  expect(kb.links.map((l: { key: string }) => l.key)).toContain(a.key);
  // Rigenerarlo non duplica la pagina.
  await pk.getByRole("button", { name: "Genera verbale" }).click();
  await expect(done).toContainText(/Il verbale c'era già/);

  // Telefono: la roadmap è una lista per mese, niente scorrimento orizzontale, bersagli da 44 px.
  await page.setViewportSize({ width: 375, height: 812 });
  await page.reload();
  await expect(page.getByRole("heading", { level: 1, name: "Cabina di portafoglio" })).toBeVisible();
  await expect(page.locator("[data-roadmap-months]")).toBeVisible();
  await page.waitForLoadState("networkidle");
  await expectNoHorizontalScroll(page);
  for (const name of ["Aggiungi 100 k€", "Togli 10 k€"]) {
    const box = (await page.getByRole("button", { name }).boundingBox())!;
    expect(box.height, name).toBeGreaterThanOrEqual(43.5);
    expect(box.width, name).toBeGreaterThanOrEqual(43.5);
  }
  const handle = (await page.getByRole("slider", { name: "Linea di taglio" }).boundingBox())!;
  expect(handle.height).toBeGreaterThanOrEqual(43.5);
  await admin.dispose();
});
