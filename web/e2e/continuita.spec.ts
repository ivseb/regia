// Continuità operativa (docs/sviluppo/continuita.md): il «cosa manca» con le incoerenze lungo le
// dipendenze, la BIA con la griglia e la proposta, il piano dei test e il livello sulla mappa,
// anche a 375 px. I dati di partenza li crea scripts/dev-demo-continuita.sh; le regole (richiesto
// ereditato, versioni, evidenze, lavoro dai rilievi) le prova internal/itest/continuita_test.go.
import { test, expect } from "@playwright/test";
import { call, expectNoHorizontalScroll, login, scegli, uniq } from "./fixtures";

test("cosa manca: incoerenze di RTO lungo le dipendenze, con l'azione e il lavoro", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/architettura/continuita");
  await expect(page.getByRole("heading", { name: "Continuità operativa" }).first()).toBeVisible();
  const missing = page.getByRole("region", { name: "Cosa manca" });
  // L'area riservata promette 1 ora ma chiama API dei clienti che ripartono in 4 ore.
  await expect(missing.getByText(/ha RTO 1 h ma dipende da .+ con RTO 4 h/).first()).toBeVisible();
  // Filtro per tipo nella query string.
  await page.goto("/architettura/continuita?tipo=dependency_slower");
  await expect(missing.getByText(/dipende da/).first()).toBeVisible();
  await expect(missing.getByText(/senza piano di continuità approvato/)).toHaveCount(0);
});

test("BIA: griglia degli impatti, proposta di MTPD e RTO, valori salvati", async ({ page }) => {
  await login(page, "admin");
  const name = uniq("Incassi");
  const cap = await call(page.request, "POST", "/api/v1/architecture/capabilities", { name });
  const bia = await call(page.request, "POST", "/api/v1/continuity/bia", { capability: cap.id });
  await page.goto(`/e/${bia.key}`);
  const section = page.getByRole("region", { name: "Impatto nel tempo" });
  await expect(section).toBeVisible();
  await scegli(section.getByLabel("Economico, dopo 4 ore"), /^4 · /);
  // La proposta si aggiorna mentre si compila: intollerabile a 4 ore → MTPD 4 h, RTO 1 h.
  await expect(section.getByText("Il primo impatto intollerabile è «Economico» dopo 4 ore.")).toBeVisible();
  await section.getByRole("button", { name: "Usa la proposta" }).click();
  await section.getByRole("button", { name: /^Salva/ }).click();
  await expect
    .poll(async () => (await call(page.request, "GET", `/api/v1/continuity/bia/${bia.key}`)).rto_minutes, { timeout: 15000 })
    .toBe(60);
  const v = await call(page.request, "GET", `/api/v1/continuity/bia/${bia.key}`);
  expect(v.mtpd_minutes).toBe(240);
  expect(v.impacts.economico["4h"]).toBe(4);
});

test("piano dei test e livello «Continuità» sulla mappa", async ({ page }) => {
  await login(page, "admin");
  // Un test pianificato oggi sui pagamenti (funzione critica dei dati demo) compare nel suo mese.
  const apps = await call(page.request, "GET", "/api/v1/architecture/applications");
  const pay = apps.items.find((a: { code: string }) => a.code === "PAY");
  const today = new Date().toISOString().slice(0, 10);
  const tst = await call(page.request, "POST", "/api/v1/continuity/tests", { subject: pay.key, type: "tabletop", planned_on: today });
  await page.goto(`/architettura/continuita/test?anno=${today.slice(0, 4)}`);
  await expect(page.getByRole("heading", { name: "Piano dei test" }).first()).toBeVisible();
  await expect(page.getByText(tst.key).first()).toBeVisible();
  await page.goto("/architettura/mappa");
  await page.getByRole("checkbox", { name: "Continuità" }).check();
  await expect(page.getByTestId("livello-continuita")).toBeAttached();
});

test("a 375 px: cosa manca, BIA e piano dei test senza scorrimento orizzontale", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 800 });
  await login(page, "admin");
  for (const path of ["/architettura/continuita", "/architettura/continuita/bia", "/architettura/continuita/test", "/configurazione/continuita"]) {
    await page.goto(path);
    await expect(page.locator("main")).toBeVisible();
    await page.waitForLoadState("networkidle");
    await expectNoHorizontalScroll(page);
  }
  const bias = await call(page.request, "GET", "/api/v1/continuity/bia");
  const any = bias.items.find((r: { bia: { key: string } | null }) => r.bia);
  await page.goto(`/e/${any.bia.key}`);
  await expect(page.getByRole("region", { name: "Impatto nel tempo" })).toBeVisible();
  await expectNoHorizontalScroll(page);
});
