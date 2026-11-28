// Pannello «Assistente», invio della domanda: la domanda entra subito nella conversazione come
// messaggio della persona, la casella si svuota e la risposta cresce sotto in streaming; se la
// domanda non arriva a una risposta torna nella casella. Anche a 375 px e con axe. Fornitore
// finto, in un'organizzazione creata apposta (l'AI accesa non cambia le pagine degli altri test).
import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import { createRequire } from "node:module";
import { apiAs, call, createTenant, expectNoHorizontalScroll, login, uniq } from "./fixtures";

const axePath = createRequire(import.meta.url).resolve("axe-core/axe.min.js");
test.use({ bypassCSP: true });

async function seriousA11y(page: Page): Promise<unknown[]> {
  await page.addScriptTag({ path: axePath });
  return page.evaluate(async () => {
    // @ts-expect-error axe è globale dopo addScriptTag
    const r = await window.axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] }, resultTypes: ["violations"] });
    return r.violations
      .filter((v: { impact: string }) => v.impact === "serious" || v.impact === "critical")
      .map((v: { id: string; help: string; nodes: { target: string[] }[] }) => ({ id: v.id, help: v.help, targets: v.nodes.slice(0, 5).map((n) => n.target) }));
  });
}

let TENANT = "";

async function enableFake(req: APIRequestContext) {
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: false });
  const v = await call<{ providers: { id: string }[] }>(req, "GET", "/api/v1/ai/settings");
  for (const p of v.providers) await call(req, "DELETE", `/api/v1/ai/providers/${p.id}`);
  const p = await call<{ id: string }>(req, "POST", "/api/v1/ai/providers", { kind: "fake", name: "Finto e2e" });
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: true, deep: { provider_id: p.id, model: "finto-approfondito" } });
}

/** Trattiene le domande all'assistente finché non si chiama il rilascio restituito. */
async function holdAsk(page: Page): Promise<() => void> {
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  await page.route("**/api/v1/ai/ask", async (route) => {
    await gate;
    await route.continue();
  });
  return release;
}

test.describe("AI: invio della domanda nel pannello", () => {
  test.describe.configure({ mode: "serial" });

  test.beforeAll(async ({ playwright, baseURL }) => {
    const { req } = await apiAs(playwright, baseURL!, "admin");
    const slug = `aiinv-${Date.now().toString(36)}${Math.floor(Math.random() * 1e4).toString(36)}`;
    await createTenant(req, uniq("Org AI invio"), slug);
    const me = await call<{ tenants: { slug: string }[] }>(req, "GET", "/api/v1/me");
    expect(me.tenants.map((t) => t.slug)).toContain(slug);
    await req.dispose();
    TENANT = slug;
  });

  test("la domanda compare subito, la casella si svuota, la risposta arriva sotto", async ({ page }) => {
    await login(page, "admin", TENANT);
    await enableFake(page.request);
    const title = uniq("Archivio fotografico");
    const el = await call<{ key: string }>(page.request, "POST", "/api/v1/demand/requests", { title, description: "Testo." });
    const release = await holdAsk(page);
    await page.goto("/da-fare");
    await page.getByTestId("apri-assistente").click();
    const panel = page.getByTestId("assistente");
    const box = panel.getByLabel("La tua domanda");
    const question = `cerca ${title}`;
    await box.fill(question);
    await box.press("Enter");

    // Prima che il server risponda: la domanda è nella conversazione e la casella è vuota.
    const asked = panel.getByTestId("domanda");
    await expect(asked).toHaveCount(1);
    await expect(asked).toContainText(question);
    await expect(asked).toHaveAttribute("data-pending", "true");
    await expect(box).toHaveValue("");
    await expect(panel.getByTestId("avanzamento")).toBeVisible();
    await expect(panel.getByRole("button", { name: "Cerco…" })).toBeDisabled();
    await expect(panel.getByText("Chiedi qualcosa sui dati")).toHaveCount(0);

    release();
    await expect(panel.getByTestId("risposta").last()).toContainText(el.key);
    // Una sola domanda, quella salvata: la bozza non resta come doppione.
    await expect(asked).toHaveCount(1);
    await expect(asked).not.toHaveAttribute("data-pending", "true");
    await expect(panel.getByTestId("risposta-in-arrivo")).toHaveCount(0);
    await expect(box).toHaveValue("");

    // La seconda domanda nella stessa conversazione si aggiunge sotto la prima.
    await box.fill(`cerca ${title} di nuovo`);
    await panel.getByRole("button", { name: "Chiedi", exact: true }).click();
    await expect(asked).toHaveCount(2);
    await expect(box).toHaveValue("");
    await expect(panel.getByTestId("risposta")).toHaveCount(2);
    await expect(asked).toHaveCount(2);
  });

  test("se la domanda non parte torna nella casella", async ({ page }) => {
    await login(page, "admin", TENANT);
    await enableFake(page.request);
    await page.route("**/api/v1/ai/ask", (route) =>
      route.fulfill({ status: 503, contentType: "application/problem+json", body: JSON.stringify({ status: 503, code: "ai.provider_down", detail: "fornitore non raggiungibile" }) }),
    );
    await page.goto("/da-fare");
    await page.getByTestId("apri-assistente").click();
    const panel = page.getByTestId("assistente");
    const box = panel.getByLabel("La tua domanda");
    await box.fill("che progetti ci sono aperti?");
    await box.press("Enter");
    await expect(panel.getByText("La domanda non è partita")).toBeVisible();
    await expect(box).toHaveValue("che progetti ci sono aperti?");
    await expect(box).toBeFocused();
    await expect(panel.getByTestId("domanda")).toHaveCount(0);
    await expect(panel.getByTestId("risposta-in-arrivo")).toHaveCount(0);
  });

  test("a 375 px la domanda in attesa sta nel pannello e passa axe", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    await login(page, "admin", TENANT);
    await enableFake(page.request);
    const release = await holdAsk(page);
    await page.goto("/da-fare");
    await page.getByRole("button", { name: "Menu" }).click();
    await page.getByTestId("apri-assistente").click();
    const panel = page.getByTestId("assistente");
    const long = `quali richieste aperte riguardano ${"il-magazzino-centrale-".repeat(6)}?`;
    await panel.getByLabel("La tua domanda").fill(long);
    await panel.getByRole("button", { name: "Chiedi", exact: true }).click();
    await expect(panel.getByTestId("domanda")).toContainText("il-magazzino-centrale-");
    await expectNoHorizontalScroll(page);
    const box = await panel.getByTestId("domanda").boundingBox();
    expect(box && box.x >= 0 && box.x + box.width <= 375).toBeTruthy();
    expect(await seriousA11y(page)).toEqual([]);
    release();
    await expect(panel.getByTestId("risposta")).toHaveCount(1);
    expect(await seriousA11y(page)).toEqual([]);
  });
});
