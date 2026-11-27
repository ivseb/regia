// Ondata 2, proposta 2 (replica/usabilita.md): ogni scheda si apre sulla sintesi giusta.
// Richiesta «decisione prima» (numeri coerenti con la graduatoria), apertura predefinita per ruolo
// invariata, applicazione 360, telefono a 375 px e una vista in inglese.
import { test, expect } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login } from "./fixtures";

const MOBILE = { width: 375, height: 812 };

type Rank = { items: { key: string; position: number }[]; cut: number | null };

test("richiesta: si apre sulla sintesi, con i numeri della graduatoria e cosa manca", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "pmo");
  const rk = await call<Rank>(req, "GET", "/api/v1/portfolio/ranking");
  const me = rk.items.find((i) => i.key === "DEM-5")!;
  expect(me).toBeTruthy();
  const sum = await call<{ rank: { position: number; total: number }; missing: { kind: string }[] }>(req, "GET", "/api/v1/elements/DEM-5/summary");
  // Una sola fonte: la posizione della sintesi è quella della graduatoria.
  expect(sum.rank.position).toBe(me.position);
  expect(sum.rank.total).toBe(rk.items.length);

  await login(page, "pmo");
  await page.goto("/e/DEM-5");
  await expect(page.getByRole("tab", { name: "Sintesi" })).toHaveAttribute("aria-selected", "true");
  const panel = page.getByRole("tabpanel");
  // Gli altri test creano richieste in parallelo: posizione e totale possono cambiare tra la
  // lettura dell'API e la pagina, quindi si controlla la forma «N di M», non i numeri.
  await expect(panel.getByText(/\d+ di \d+/).first()).toBeVisible();
  await expect(panel.getByRole("heading", { name: "Cosa serve per andare avanti" })).toBeVisible();
  await expect(panel.getByRole("heading", { name: "I numeri per decidere" })).toBeVisible();
  await expect(panel.getByText("Decisione in attesa")).toBeVisible();
  // Il pulsante porta alla scheda giusta.
  await panel.getByRole("button", { name: "Apri la decisione" }).click();
  await expect(page.getByRole("tab", { name: /Approvazioni/ })).toHaveAttribute("aria-selected", "true");
});

test("apertura predefinita per ruolo: la decisione in attesa batte la sintesi", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/e/DEM-3");
  await expect(page.getByRole("tab", { name: /Approvazioni/ })).toHaveAttribute("aria-selected", "true");
  // Con la scheda indicata nel link vale il link.
  await page.goto("/e/DEM-3?scheda=sintesi");
  await expect(page.getByRole("tab", { name: "Sintesi" })).toHaveAttribute("aria-selected", "true");
  // I link ai commenti (notifiche) aprono l'attività.
  await page.goto("/e/DEM-3#commenti");
  await expect(page.getByRole("tab", { name: "Attività" })).toHaveAttribute("aria-selected", "true");
});

test("applicazione 360: ciò che richiede azione in cima, numeri uguali alle pagine di dettaglio", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const dash = await call<{ kpis: { critical_open: number; overdue: number } }>(req, "GET", "/api/v1/devsecops/vuln/dashboard?application=ARC-6");
  const a = await call<{ vulns: { critical_open: number; overdue: number }; actions: { kind: string }[] }>(req, "GET", "/api/v1/architecture/applications/ARC-6/360");
  expect(a.vulns.critical_open).toBe(dash.kpis.critical_open);
  expect(a.vulns.overdue).toBe(dash.kpis.overdue);

  await login(page, "admin");
  await page.goto("/e/ARC-6");
  await expect(page.getByRole("tab", { name: "Sintesi" })).toHaveAttribute("aria-selected", "true");
  const panel = page.getByRole("tabpanel");
  const attention = panel.getByRole("region", { name: "Richiede azione" });
  await expect(attention).toBeVisible();
  if (dash.kpis.critical_open > 0) await expect(attention.getByText(/vulnerabilit/).first()).toBeVisible();
  for (const name of ["Vulnerabilità e SLA", "Ultimi rilasci", "Gate di rilascio", "Controlli e conformità", "Rischi", "Incidenti", "Dipendenze", "Richieste e impatti aperti"]) {
    await expect(panel.getByRole("region", { name, exact: true })).toBeVisible();
  }
  // La mappa piccola mostra i nomi sempre.
  await expect(panel.getByRole("group", { name: /Mappa delle dipendenze/ })).toContainText("Backend API clienti");
  // Un'azione porta al dettaglio.
  await panel.getByRole("region", { name: "Ultimi rilasci" }).getByRole("button", { name: "Servizi e rilasci" }).click();
  await expect(page.getByRole("tab", { name: "Servizi e rilasci" })).toHaveAttribute("aria-selected", "true");
});

test("telefono a 375 px: la sintesi è subito in vista e senza scorrimento laterale", async ({ page }) => {
  await page.setViewportSize(MOBILE);
  await login(page, "pmo");
  await page.goto("/e/DEM-5");
  const panel = page.getByRole("tabpanel");
  await expect(panel.getByRole("heading", { name: "Cosa serve per andare avanti" })).toBeVisible();
  await expect(panel.getByRole("button", { name: "Apri la decisione" })).toBeInViewport();
  const box = await panel.getByRole("button", { name: "Apri la decisione" }).boundingBox();
  expect(box!.height).toBeGreaterThanOrEqual(43.5);
  await expectNoHorizontalScroll(page);

  await login(page, "admin");
  await page.goto("/e/ARC-6");
  await expect(page.getByRole("tabpanel").getByRole("region", { name: "Richiede azione" })).toBeVisible();
  await expect(page.getByRole("tabpanel").getByRole("region", { name: "Rischi", exact: true })).toBeVisible();
  await expectNoHorizontalScroll(page);
});

test.describe("in inglese", () => {
  test.use({ locale: "en-US" });
  test("richiesta e applicazione", async ({ page }) => {
    await login(page, "pmo");
    await page.goto("/e/DEM-5");
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    const panel = page.getByRole("tabpanel");
    await expect(page.getByRole("tab", { name: "Summary" })).toHaveAttribute("aria-selected", "true");
    await expect(panel.getByRole("heading", { name: "What's needed to move forward" })).toBeVisible();
    await expect(panel.getByRole("heading", { name: "The numbers to decide on" })).toBeVisible();
    await expect(panel.getByText(/^\d+ of \d+$/)).toBeVisible();
    await page.goto("/e/ARC-6");
    await expect(page.getByRole("tabpanel").getByRole("region", { name: "Needs action" })).toBeVisible();
    await expect(page.getByRole("tabpanel").getByRole("region", { name: "Latest releases" })).toBeVisible();
    const text = await page.getByRole("tabpanel").innerText();
    expect(text).not.toMatch(/\b(Apri|Dettaglio|Tocca a|Nessun[aoi]?)\b/);
  });
});
