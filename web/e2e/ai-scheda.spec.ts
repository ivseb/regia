// AI, ondata 3 (replica/ai.md): assistente nella scheda (riassunto, cosa manca per decidere con i
// link, bozza riletta e inserita) e alla creazione (duplicati probabili, campi accettati uno per
// uno, asset da citare). Senza AI la scheda e il form restano identici. Tutto con il fornitore
// finto, in un'organizzazione creata apposta: niente cambia per gli altri test.
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

async function touchTargets(page: Page, scope: string) {
  const boxes = await page.getByTestId(scope).locator("button:visible, a:visible").evaluateAll((els) =>
    els.map((e) => ({ name: (e.textContent ?? "").trim().slice(0, 40), h: e.getBoundingClientRect().height })),
  );
  for (const b of boxes) expect(b.h, `altezza di «${b.name}»`).toBeGreaterThanOrEqual(43.5);
}

let TENANT = "";

async function setAI(req: APIRequestContext, on: boolean) {
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: false });
  if (!on) return;
  const v = await call<{ providers: { id: string }[] }>(req, "GET", "/api/v1/ai/settings");
  for (const p of v.providers) await call(req, "DELETE", `/api/v1/ai/providers/${p.id}`);
  const p = await call<{ id: string }>(req, "POST", "/api/v1/ai/providers", { kind: "fake", name: "Finto e2e" });
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: true, deep: { provider_id: p.id, model: "finto-approfondito" } });
}

test.describe("AI, ondata 3: assistente nella scheda", () => {
  test.describe.configure({ mode: "serial" });

  test.beforeAll(async ({ playwright, baseURL }) => {
    const { req } = await apiAs(playwright, baseURL!, "admin");
    const slug = `ai3-${Date.now().toString(36)}${Math.floor(Math.random() * 1e4).toString(36)}`;
    await createTenant(req, uniq("Org AI 3"), slug);
    await req.dispose();
    TENANT = slug;
  });

  test("senza AI la scheda e il form non cambiano", async ({ page }) => {
    await login(page, "admin", TENANT);
    await setAI(page.request, false);
    const el = await call<{ key: string }>(page.request, "POST", "/api/v1/demand/requests", { title: uniq("Senza AI"), description: "Testo." });
    await page.goto(`/e/${el.key}`);
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await expect(page.getByTestId("assistente-scheda")).toHaveCount(0);
    await page.goto("/richieste/nuova");
    await expect(page.getByLabel("Titolo")).toBeVisible();
    await expect(page.getByTestId("assistente-creazione")).toHaveCount(0);
  });

  test("riassunto, cosa manca con il link al commento, bozza riletta e pubblicata", async ({ page }) => {
    await login(page, "admin", TENANT);
    await setAI(page.request, true);
    const title = uniq("Archivio dei verbali");
    const el = await call<{ key: string }>(page.request, "POST", "/api/v1/demand/requests", { title, description: "Breve." });
    await call(page.request, "POST", `/api/v1/elements/${el.key}/comments`, { body: "Chi approva la spesa?" });
    await page.goto(`/e/${el.key}`);
    const box = page.getByTestId("assistente-scheda");
    await expect(box).toBeVisible();

    await box.getByRole("button", { name: "Riassumi" }).click();
    await expect(box.getByTestId("risposta-scheda")).toContainText(`${el.key} «${title}»`);

    await box.getByRole("button", { name: "Cosa manca per decidere" }).click();
    const gaps = box.getByRole("list", { name: "Cose che mancano o non tornano" });
    await expect(gaps).toContainText("La descrizione è molto breve");
    await expect(gaps).toContainText("Domanda aperta");
    await gaps.getByRole("button", { name: /Vai al commento di/ }).click();
    await expect(page.getByRole("tab", { name: "Attività" })).toHaveAttribute("aria-selected", "true");
    await expect(page.getByText("Chi approva la spesa?")).toBeInViewport();

    await box.getByRole("button", { name: "Scrivi una bozza" }).click();
    await box.getByRole("radio", { name: "Commento" }).check();
    await box.getByLabel("Indicazioni (facoltative)").fill("Serve la firma del direttore.");
    await box.getByRole("button", { name: "Scrivi la bozza" }).click();
    const draft = box.getByLabel("Bozza del commento");
    await expect(draft).toHaveValue(new RegExp(`Aggiornamento su ${el.key}.*Serve la firma del direttore\\.`));
    // Niente si scrive finché la persona non preme il pulsante.
    let comments = await call<{ items: { body: string }[] }>(page.request, "GET", `/api/v1/elements/${el.key}/comments`);
    expect(comments.items).toHaveLength(1);
    await draft.fill("Riletto: serve la firma del direttore entro venerdì.");
    await box.getByRole("button", { name: "Pubblica come commento" }).click();
    await expect(box.getByRole("status").filter({ hasText: "Commento pubblicato." })).toBeVisible();
    await expect(page.getByRole("region", { name: "Attività" }).getByText("Riletto: serve la firma del direttore entro venerdì.")).toBeVisible();
    comments = await call(page.request, "GET", `/api/v1/elements/${el.key}/comments`);
    expect(comments.items).toHaveLength(2);
    const usage = await call<{ by_feature: { key: string; calls: number }[] }>(page.request, "GET", "/api/v1/ai/usage");
    for (const f of ["riassunti", "mancanze", "bozze"]) expect(usage.by_feature.find((x) => x.key === f)?.calls, f).toBeGreaterThanOrEqual(1);
  });

  test("alla creazione: duplicato probabile, campi accettati uno per uno", async ({ page }) => {
    await login(page, "admin", TENANT);
    await setAI(page.request, true);
    const word = uniq("Protocollo").replace(/\W/g, "");
    const old = await call<{ key: string }>(page.request, "POST", "/api/v1/demand/requests", { title: `Gestione ${word} documentale`, description: "Esistente." });
    await page.goto("/richieste/nuova");
    await page.getByLabel("Titolo").fill(`Nuova gestione ${word} documentale`);
    await page.getByLabel("Bisogno e obiettivo").fill("Urgenza alta, motivo normativo.");
    const box = page.getByTestId("assistente-creazione");
    await box.getByRole("button", { name: "Cerca duplicati e proponi i campi" }).click();
    await expect(box.getByRole("list", { name: "Forse esiste già" }).getByRole("link", { name: old.key })).toBeVisible();
    const fields = box.getByRole("list", { name: "Valori proposti" });
    await fields.getByRole("button", { name: "Accetta Urgenza: Alta" }).click();
    await expect(page.getByRole("group", { name: "Urgenza" }).getByRole("button", { name: "Alta" })).toHaveAttribute("aria-pressed", "true");
    await expect(fields.getByRole("button", { name: /Accetta Driver/ })).toBeVisible();
    // Il driver non si accetta: resta vuoto. Si crea solo con il pulsante della persona.
    await page.getByRole("button", { name: "Crea in bozza" }).click();
    await expect(page).toHaveURL(/\/e\/[A-Z]+-\d+/);
    const key = page.url().split("/e/")[1];
    const d = await call<{ demand: { driver: string | null }; custom_fields: { key: string; value: unknown }[] }>(page.request, "GET", `/api/v1/elements/${key}`);
    expect(d.demand.driver).toBeNull();
    expect(d.custom_fields.find((f) => f.key === "urgenza")?.value).toBeTruthy();
  });

  test("a 375 px e con axe: scheda con le risposte e form con le proposte", async ({ page }) => {
    await login(page, "admin", TENANT);
    await setAI(page.request, true);
    const el = await call<{ key: string }>(page.request, "POST", "/api/v1/demand/requests", { title: uniq("Mobile scheda"), description: "Breve." });
    await call(page.request, "POST", `/api/v1/elements/${el.key}/comments`, { body: "Quando parte?" });
    await page.goto(`/e/${el.key}`);
    const box = page.getByTestId("assistente-scheda");
    await box.getByRole("button", { name: "Cosa manca per decidere" }).click();
    await expect(box.getByTestId("mancanze")).toBeVisible();
    await box.getByRole("button", { name: "Scrivi una bozza" }).click();
    await page.waitForLoadState("networkidle");
    expect(await seriousA11y(page)).toEqual([]);
    await page.setViewportSize({ width: 375, height: 812 });
    await expectNoHorizontalScroll(page);
    await touchTargets(page, "assistente-scheda");

    await page.goto("/richieste/nuova");
    await page.getByLabel("Titolo").fill(uniq("Mobile scheda"));
    await page.getByLabel("Bisogno e obiettivo").fill("Urgenza alta.");
    const form = page.getByTestId("assistente-creazione");
    await form.getByRole("button", { name: "Cerca duplicati e proponi i campi" }).click();
    await expect(form.getByRole("list", { name: "Valori proposti" })).toBeVisible();
    await expectNoHorizontalScroll(page);
    await touchTargets(page, "assistente-creazione");
    await page.setViewportSize({ width: 1400, height: 900 });
    expect(await seriousA11y(page)).toEqual([]);
  });
});
