// AI, ondata 2 (replica/ai.md): domande sul grafo nel pannello «Assistente» (query visibile,
// modificabile, salvata come vista, corretta una volta se non è valida), risposta in streaming,
// server MCP acceso dall'amministratore e usato con un token API, avviso del budget come
// notifica. Tutto con il fornitore finto, in un'organizzazione creata apposta: l'AI accesa e
// le richieste create non cambiano le pagine degli altri test (né «demo» né «altro»).
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

async function enableFake(req: APIRequestContext, extra: Record<string, unknown> = {}) {
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: false });
  const v = await call<{ providers: { id: string }[] }>(req, "GET", "/api/v1/ai/settings");
  for (const p of v.providers) await call(req, "DELETE", `/api/v1/ai/providers/${p.id}`);
  const p = await call<{ id: string }>(req, "POST", "/api/v1/ai/providers", { kind: "fake", name: "Finto e2e" });
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: true, deep: { provider_id: p.id, model: "finto-approfondito" }, ...extra });
}

async function openGraph(page: Page) {
  await page.getByTestId("apri-assistente").click();
  const panel = page.getByTestId("assistente");
  await panel.getByRole("radio", { name: "Domanda sul grafo" }).click();
  return panel;
}

async function askGraph(page: Page, question: string) {
  const panel = page.getByTestId("assistente");
  await panel.getByLabel("La tua domanda sul grafo").fill(question);
  await panel.getByRole("button", { name: "Chiedi", exact: true }).click();
  return panel.getByTestId("risposta-grafo").last();
}

test.describe("AI, ondata 2: domande sul grafo, streaming, MCP", () => {
  test.describe.configure({ mode: "serial" });

  test.beforeAll(async ({ playwright, baseURL }) => {
    const { req } = await apiAs(playwright, baseURL!, "admin");
    const slug = `ai2-${Date.now().toString(36)}${Math.floor(Math.random() * 1e4).toString(36)}`;
    await createTenant(req, uniq("Org AI 2"), slug);
    const me = await call<{ tenants: { slug: string }[] }>(req, "GET", "/api/v1/me");
    expect(me.tenants.map((t) => t.slug)).toContain(slug);
    await req.dispose();
    TENANT = slug;
  });

  test("una domanda diventa una query: elenco con i link, query modificabile, vista salvata", async ({ page }) => {
    await login(page, "admin", TENANT);
    await enableFake(page.request);
    const title = uniq("Portale fornitori");
    const el = await call<{ key: string }>(page.request, "POST", "/api/v1/demand/requests", { title, description: "Richiesta per le domande sul grafo." });
    await page.goto("/da-fare");
    await openGraph(page);
    const answer = await askGraph(page, "Quali richieste aperte ci sono?");
    const query = answer.getByTestId("query-grafo");
    await expect(query).toHaveValue("tipo: business_request fase: aperta");
    await expect(answer.getByRole("list", { name: "Elementi trovati" }).getByRole("link", { name: new RegExp(el.key) })).toBeVisible();

    // La query si cambia a mano e si riesegue, senza AI.
    await query.fill(`tipo: business_request "${title}"`);
    await answer.getByRole("button", { name: "Esegui" }).click();
    await expect(answer.getByRole("status").filter({ hasText: "1 elemento" })).toBeVisible();

    // Salvata come vista, si riapre nell'elenco delle domande sul grafo.
    await answer.getByLabel("Nome della vista").fill(uniq("Vista dal grafo"));
    await answer.getByRole("button", { name: "Salva come vista" }).click();
    await expect(answer.getByText("Vista salvata.")).toBeVisible();
    await answer.getByRole("link", { name: "Aprila" }).click();
    await expect(page).toHaveURL(/\/elenco\/domande\?v=/);
    await expect(page.getByRole("heading", { name: "Domande sul grafo" })).toBeVisible();
    await expect(page.getByRole("link", { name: el.key }).first()).toBeVisible();
  });

  test("una query non valida si corregge una volta e lo si vede", async ({ page }) => {
    await login(page, "admin", TENANT);
    await enableFake(page.request);
    await page.goto("/da-fare");
    await openGraph(page);
    const answer = await askGraph(page, "Quali applicazioni critiche abbiamo?");
    await expect(answer.getByTestId("query-grafo")).toHaveValue("tipo: application");
    await expect(answer.getByRole("note")).toContainText("La prima query");
    // Due chiamate per la funzione «domande_grafo» nel consumo.
    const usage = await call<{ by_feature: { key: string; calls: number }[] }>(page.request, "GET", "/api/v1/ai/usage");
    expect(usage.by_feature.find((f) => f.key === "domande_grafo")?.calls).toBeGreaterThanOrEqual(2);
  });

  test("la risposta dell'assistente arriva in streaming", async ({ page }) => {
    await login(page, "admin", TENANT);
    await enableFake(page.request);
    const title = uniq("Archivio documentale");
    const el = await call<{ key: string }>(page.request, "POST", "/api/v1/demand/requests", { title, description: "Testo." });
    await page.goto("/da-fare");
    await page.getByTestId("apri-assistente").click();
    const panel = page.getByTestId("assistente");
    const streamed = page.waitForRequest((r) => r.url().endsWith("/api/v1/ai/ask") && (r.headers()["accept"] ?? "").includes("text/event-stream"));
    await panel.getByLabel("La tua domanda").fill(`cerca ${title}`);
    await panel.getByRole("button", { name: "Chiedi", exact: true }).click();
    await streamed;
    await expect(panel.getByTestId("risposta").last()).toContainText(el.key);
  });

  test("l'amministratore accende il server MCP e un client lo usa con un token", async ({ page }) => {
    await login(page, "admin", TENANT);
    await enableFake(page.request);
    const title = uniq("Migrazione MCP");
    const el = await call<{ key: string }>(page.request, "POST", "/api/v1/demand/requests", { title, description: "Testo." });
    const token = (await call<{ token: string }>(page.request, "POST", "/api/v1/me/tokens", { name: "Claude Code" })).token;
    const mcp = (body: unknown) =>
      page.request.post("/api/v1/mcp", { data: body, headers: { Authorization: `Bearer ${token}`, Accept: "application/json, text/event-stream" } });
    const list = { jsonrpc: "2.0", id: 1, method: "tools/list" };
    expect((await mcp(list)).status()).toBe(403);

    await page.goto("/configurazione/ai");
    await expect(page.getByTestId("mcp-indirizzo")).toContainText("/api/v1/mcp");
    await page.getByLabel("Server MCP acceso").check();
    await page.getByRole("button", { name: "Salva le impostazioni" }).click();
    await expect(page.getByText("Impostazioni salvate.")).toBeVisible();

    const tools = await (await mcp(list)).json();
    const names = (tools.result.tools as { name: string }[]).map((t) => t.name);
    expect(names).toEqual(expect.arrayContaining(["cerca", "leggi", "commenta", "conferma", "rifiuta"]));
    const res = await (await mcp({ jsonrpc: "2.0", id: 2, method: "tools/call", params: { name: "cerca", arguments: { testo: title } } })).json();
    expect(JSON.stringify(res.result.content)).toContain(el.key);
    const usage = await call<{ by_feature: { key: string }[]; recent: { feature: string; provider: string }[] }>(page.request, "GET", "/api/v1/ai/usage");
    expect(usage.recent.some((c) => c.feature === "mcp" && c.provider === "client")).toBeTruthy();
    await page.goto("/configurazione/ai/consumo");
    await expect(page.getByText("Server MCP").first()).toBeVisible();
  });

  test("all'80% del budget chi amministra riceve una notifica", async ({ page }) => {
    await login(page, "admin", TENANT);
    // Un tetto appena sopra i token già usati nel mese: la prossima risposta lo supera e scatta
    // l'avviso del mese (una volta sola).
    const used = (await call<{ budget: { tokens: number } }>(page.request, "GET", "/api/v1/ai/usage")).budget.tokens;
    await enableFake(page.request, { monthly_token_budget: used + 1 });
    // Una domanda sul grafo fa una sola chiamata: passa, e dopo il tetto è superato.
    await call(page.request, "POST", "/api/v1/ai/graph", { question: "Quali richieste aperte ci sono?" });
    await page.goto("/notifiche");
    const row = page.getByRole("link", { name: /Il budget dell'AI di/ }).first();
    await expect(row).toBeVisible();
    await row.click();
    await expect(page).toHaveURL(/\/configurazione\/ai\/consumo$/);
    await enableFake(page.request);
  });

  test("a 375 px e con axe: pannello delle domande sul grafo ed elenco", async ({ page }) => {
    await login(page, "admin", TENANT);
    await enableFake(page.request);
    await page.goto("/configurazione/ai");
    await expect(page.getByRole("heading", { level: 1, name: "Intelligenza artificiale" })).toBeVisible();
    await page.waitForLoadState("networkidle");
    expect(await seriousA11y(page)).toEqual([]);
    await page.setViewportSize({ width: 375, height: 812 });
    await expectNoHorizontalScroll(page);
    await page.goto("/elenco/domande");
    await expect(page.getByRole("heading", { name: "Domande sul grafo" })).toBeVisible();
    await expectNoHorizontalScroll(page);
    await page.goto("/da-fare");
    await page.getByRole("button", { name: "Menu" }).click();
    await openGraph(page);
    const answer = await askGraph(page, "Quali richieste aperte ci sono?");
    await expect(answer.getByTestId("query-grafo")).toBeVisible();
    await expectNoHorizontalScroll(page);
    expect(await seriousA11y(page)).toEqual([]);
  });
});
