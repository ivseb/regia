// AI, ondata 1 (replica/ai.md): l'amministratore configura il fornitore finto (solo in sviluppo,
// niente rete né chiavi) e accende l'AI; la persona fa una domanda nel pannello «Assistente» e
// arriva all'elemento dal link; una scrittura resta una proposta finché non si conferma; il
// consumo mostra le chiamate; spenta, il pannello sparisce. Lavora nel tenant «altro», così
// l'AI accesa non cambia le pagine degli altri test, che girano nel tenant demo.
import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import { createRequire } from "node:module";
import { call, expectNoHorizontalScroll, login, scegli, uniq } from "./fixtures";

// axe-core come in a11y.spec.ts: la CSP dell'app blocca lo script iniettato, solo nei test la si ignora.
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

const TENANT = "altro";

async function resetAI(req: APIRequestContext) {
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: false });
  const v = await call<{ providers: { id: string }[] }>(req, "GET", "/api/v1/ai/settings");
  for (const p of v.providers) await call(req, "DELETE", `/api/v1/ai/providers/${p.id}`);
}

async function enableFake(req: APIRequestContext) {
  await resetAI(req);
  const p = await call<{ id: string }>(req, "POST", "/api/v1/ai/providers", { kind: "fake", name: "Finto e2e" });
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: true, deep: { provider_id: p.id, model: "finto-approfondito" } });
}

async function newRequest(req: APIRequestContext, title: string) {
  const c = await call<{ id: string; key: string }>(req, "POST", "/api/v1/demand/requests", { title, description: "Richiesta per l'assistente." });
  return c;
}

async function ask(page: Page, question: string) {
  const panel = page.getByTestId("assistente");
  await panel.getByLabel("La tua domanda").fill(question);
  await panel.getByRole("button", { name: "Chiedi", exact: true }).click();
  return panel;
}

test.describe("AI: fondamenta e assistente", () => {
  test.describe.configure({ mode: "serial" });

  test("l'amministratore accende l'AI e l'assistente risponde con il link all'elemento", async ({ page }) => {
    await login(page, "admin", TENANT);
    await resetAI(page.request);
    const title = uniq("Revisione del magazzino");
    const el = await newRequest(page.request, title);

    await page.goto("/configurazione/ai");
    await expect(page.getByRole("heading", { name: "Intelligenza artificiale" })).toBeVisible();
    await expect(page.getByTestId("apri-assistente")).toHaveCount(0);
    await page.getByRole("button", { name: "Aggiungi fornitore" }).click();
    await scegli(page.getByLabel("Tipo"), "Fornitore finto (solo sviluppo)");
    await page.getByLabel("Nome", { exact: true }).fill("Finto e2e");
    await page.getByRole("button", { name: "Aggiungi fornitore" }).click();
    await expect(page.getByTestId("fornitore").filter({ hasText: "Finto e2e" })).toBeVisible();

    const deep = page.getByTestId("livello-approfondito");
    await scegli(deep.getByLabel("Fornitore"), "Finto e2e");
    await expect(deep.getByLabel("Modello")).toHaveValue("finto-approfondito");
    await page.getByLabel("AI accesa").check();
    await page.getByRole("button", { name: "Salva le impostazioni" }).click();
    await expect(page.getByText("Impostazioni salvate.")).toBeVisible();

    await page.getByTestId("apri-assistente").click();
    const panel = await ask(page, `cerca ${title}`);
    const answer = panel.getByTestId("risposta").last();
    await expect(answer).toContainText(el.key);
    await answer.getByRole("link", { name: el.key, exact: true }).first().click();
    await expect(page).toHaveURL(new RegExp(`/e/${el.key}$`));
    await expect(page.getByRole("heading", { name: title })).toBeVisible();

    // Il consumo mostra le due chiamate (cerca, poi la risposta).
    await page.goto("/configurazione/ai/consumo");
    await expect(page.getByRole("heading", { name: "Consumo dell'AI" })).toBeVisible();
    await expect(page.getByTestId("consumo-chiamate").getByRole("row").filter({ hasText: "finto-approfondito" }).first()).toBeVisible();
  });

  test("una scrittura resta una proposta finché la persona non la conferma", async ({ page }) => {
    await login(page, "admin", TENANT);
    await enableFake(page.request);
    const el = await newRequest(page.request, uniq("Richiesta da commentare"));
    const text = uniq("Serve il parere della sicurezza");
    await page.goto(`/e/${el.key}`);
    await page.getByTestId("apri-assistente").click();
    const panel = await ask(page, `commenta ${el.key}: ${text}`);
    const card = panel.getByTestId("proposta");
    await expect(card).toContainText(text);
    await expect(card).toContainText("In attesa della tua decisione.");
    const before = await call<unknown[]>(page.request, "GET", `/api/v1/elements/${el.id}/comments`);
    expect(JSON.stringify(before)).not.toContain(text);
    await card.getByRole("button", { name: "Conferma" }).click();
    await expect(card).toContainText("Confermata e applicata.");
    const after = await call<unknown>(page.request, "GET", `/api/v1/elements/${el.id}/comments`);
    expect(JSON.stringify(after)).toContain(text);
  });

  test("a 375 px pannello e configurazione non scorrono in orizzontale", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    await login(page, "admin", TENANT);
    await enableFake(page.request);
    await page.goto("/configurazione/ai");
    await expect(page.getByRole("heading", { name: "Intelligenza artificiale" })).toBeVisible();
    await expectNoHorizontalScroll(page);
    await page.goto("/configurazione/ai/consumo");
    await expect(page.getByRole("heading", { name: "Consumo dell'AI" })).toBeVisible();
    await expectNoHorizontalScroll(page);
    await page.goto("/da-fare");
    await page.getByRole("button", { name: "Menu" }).click();
    await page.getByTestId("apri-assistente").click();
    const panel = await ask(page, "cerca magazzino");
    await expect(panel.getByTestId("risposta").last()).toBeVisible();
    await expectNoHorizontalScroll(page);
    await panel.getByRole("button", { name: "Chiudi" }).click();
    await expect(page.getByTestId("assistente")).toHaveCount(0);
  });

  test("accessibilità: configurazione, consumo e pannello", async ({ page }) => {
    await login(page, "admin", TENANT);
    await enableFake(page.request);
    await page.goto("/configurazione/ai");
    await expect(page.getByRole("heading", { level: 1, name: "Intelligenza artificiale" })).toBeVisible();
    await page.waitForLoadState("networkidle");
    expect(await seriousA11y(page)).toEqual([]);
    await page.goto("/configurazione/ai/consumo");
    await expect(page.getByRole("heading", { level: 1, name: "Consumo dell'AI" })).toBeVisible();
    await page.waitForLoadState("networkidle");
    expect(await seriousA11y(page)).toEqual([]);
    await page.getByTestId("apri-assistente").click();
    const panel = await ask(page, "cerca magazzino");
    await expect(panel.getByTestId("risposta").last()).toBeVisible();
    expect(await seriousA11y(page)).toEqual([]);
  });

  test("spenta, il pannello sparisce e il resto funziona", async ({ page }) => {
    await login(page, "admin", TENANT);
    await enableFake(page.request);
    await call(page.request, "PUT", "/api/v1/ai/settings", { enabled: false });
    const el = await newRequest(page.request, uniq("Richiesta senza AI"));
    await page.goto(`/e/${el.key}`);
    await expect(page.getByRole("heading", { name: /Richiesta senza AI/ })).toBeVisible();
    await expect(page.getByTestId("apri-assistente")).toHaveCount(0);
    const r = await page.request.post("/api/v1/ai/ask", { data: { question: "ciao" } });
    expect(r.status()).toBe(409);
  });
});
