// AI, ondata 4 (replica/ai.md): la wiki personale nel profilo. Senza AI si scrive a mano (pagina
// con i link agli elementi, modifica, differenze, annullamento); «aggiungi alla wiki» dalla scheda e
// da una risposta dell'assistente; con il fornitore finto l'assistente aggiorna la wiki e la sua
// modifica si annulla dal registro. Tutto in un'organizzazione creata apposta.
import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import { createRequire } from "node:module";
import { apiAs, call, createTenant, expectNoHorizontalScroll, login, uniq } from "./fixtures";

const axePath = createRequire(import.meta.url).resolve("axe-core/axe.min.js");
test.use({ bypassCSP: true });

async function seriousA11y(page: Page): Promise<unknown[]> {
  await page.addScriptTag({ path: axePath });
  return page.evaluate(async () => {
    // @ts-expect-error axe è globale dopo addScriptTag
    const r = await window.axe.run(document, {
      runOnly: {
        type: "tag",
        values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"],
      },
      resultTypes: ["violations"],
    });
    return r.violations
      .filter((v: { impact: string }) => v.impact === "serious" || v.impact === "critical")
      .map((v: { id: string; help: string; nodes: { target: string[] }[] }) => ({
        id: v.id,
        help: v.help,
        targets: v.nodes.slice(0, 5).map((n) => n.target),
      }));
  });
}

async function touchTargets(page: Page, scope: string) {
  const boxes = await page
    .getByTestId(scope)
    .locator("button:visible, a:visible")
    .evaluateAll((els) =>
      els.map((e) => ({
        name: (e.textContent ?? "").trim().slice(0, 40),
        h: e.getBoundingClientRect().height,
      })),
    );
  for (const b of boxes) expect(b.h, `altezza di «${b.name}»`).toBeGreaterThanOrEqual(43.5);
}

let TENANT = "";

async function setAI(req: APIRequestContext, on: boolean) {
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: false });
  if (!on) return;
  const v = await call<{ providers: { id: string }[] }>(req, "GET", "/api/v1/ai/settings");
  for (const p of v.providers) await call(req, "DELETE", `/api/v1/ai/providers/${p.id}`);
  const p = await call<{ id: string }>(req, "POST", "/api/v1/ai/providers", {
    kind: "fake",
    name: "Finto e2e",
  });
  await call(req, "PUT", "/api/v1/ai/settings", {
    enabled: true,
    deep: { provider_id: p.id, model: "finto-approfondito" },
  });
}

test.describe("AI, ondata 4: wiki personale", () => {
  test.describe.configure({ mode: "serial" });

  test.beforeAll(async ({ playwright, baseURL }) => {
    const { req } = await apiAs(playwright, baseURL!, "admin");
    const slug = `ai4-${Date.now().toString(36)}${Math.floor(Math.random() * 1e4).toString(36)}`;
    await createTenant(req, uniq("Org AI 4"), slug);
    await req.dispose();
    TENANT = slug;
  });

  test("senza AI: pagina scritta a mano, link, differenze e annullamento", async ({ page }) => {
    await login(page, "admin", TENANT);
    await setAI(page.request, false);
    const el = await call<{ key: string }>(page.request, "POST", "/api/v1/demand/requests", { title: uniq("Archivio dei verbali"), description: "Testo." });
    await page.goto("/profilo");
    await page.getByRole("link", { name: "Apri la mia wiki" }).click();
    await expect(page.getByRole("heading", { name: "La mia wiki", level: 1 })).toBeVisible();
    await expect(page.getByText("Senza l'AI accesa la wiki si scrive a mano.")).toBeVisible();
    await expect(page.getByTestId("wiki-assistente")).toHaveCount(0);

    await page.getByRole("button", { name: "Nuova pagina" }).click();
    await page.getByLabel("Titolo").fill("Comitato IT");
    await page.getByLabel("Testo (Markdown)").fill(`## Da seguire\n\n- ${el.key}: verbali`);
    await page.getByRole("button", { name: "Crea la pagina" }).click();
    const box = page.getByTestId("wiki-pagina");
    await expect(page.getByRole("heading", { name: "Comitato IT", level: 1 })).toBeVisible();
    await expect(box.getByRole("link", { name: el.key })).toHaveAttribute("href", `/e/${el.key}`);

    await box.getByRole("button", { name: "Modifica" }).click();
    await box.getByLabel("Testo (Markdown)").fill(`## Da seguire\n\n- ${el.key}: verbali\n- Decisione: approvato il budget`);
    await box.getByRole("button", { name: "Salva" }).click();
    await expect(box.locator(".kb-page").getByText("Decisione: approvato il budget")).toBeVisible();

    await box.getByRole("button", { name: "Storia" }).click();
    const hist = box.getByRole("list", { name: "Versioni della pagina" });
    await expect(hist.getByRole("listitem")).toHaveCount(2);
    await hist.getByRole("listitem").first().getByRole("button", { name: "Differenze" }).click();
    await expect(box.getByRole("region", { name: "Differenze della versione 2" })).toContainText("+ - Decisione: approvato il budget");
    await hist.getByRole("listitem").first().getByRole("button", { name: "Annulla" }).click();
    await expect(box.locator(".kb-page").getByText("Decisione: approvato il budget")).toHaveCount(0);
    await expect(hist.getByRole("listitem")).toHaveCount(3);

    await box.getByRole("link", { name: /La mia wiki/ }).click();
    const log = page.getByRole("list", {
      name: "Modifiche recenti della wiki",
    });
    await expect(log.getByRole("listitem").first()).toContainText("Modifica annullata");
    await expect(log.getByRole("listitem").first()).toContainText("Comitato IT");
  });

  test("aggiungi alla wiki dalla scheda", async ({ page }) => {
    await login(page, "admin", TENANT);
    const title = uniq("Portale fornitori");
    const el = await call<{ key: string }>(page.request, "POST", "/api/v1/demand/requests", { title, description: "Testo." });
    await page.goto(`/e/${el.key}`);
    const add = page.getByTestId("aggiungi-wiki");
    await add.getByRole("button", { name: "Aggiungi alla wiki" }).click();
    await add.getByLabel("Nota (facoltativa)").fill("da portare al comitato");
    await add.getByRole("button", { name: "Aggiungi", exact: true }).click();
    await expect(add.getByRole("status")).toContainText("Aggiunto a «Appunti».");
    await add.getByRole("link", { name: "Apri la pagina" }).click();
    await expect(page.getByRole("heading", { name: "Appunti", level: 1 })).toBeVisible();
    await expect(page.getByTestId("wiki-pagina").getByRole("link", { name: el.key })).toBeVisible();
    await expect(page.getByTestId("wiki-pagina")).toContainText("da portare al comitato");
  });

  test("con l'AI: l'assistente aggiorna la wiki, la modifica si annulla dal registro", async ({ page }) => {
    await login(page, "admin", TENANT);
    await setAI(page.request, true);
    const el = await call<{ key: string }>(page.request, "POST", "/api/v1/demand/requests", { title: uniq("Firma digitale"), description: "Testo." });

    // La risposta dell'assistente si aggiunge alla wiki.
    await page.goto("/profilo/wiki");
    await page.getByTestId("apri-assistente").click();
    const panel = page.getByTestId("assistente");
    await panel.getByLabel("La tua domanda").fill(`cerca Firma digitale`);
    await panel.getByRole("button", { name: "Chiedi", exact: true }).click();
    const answer = panel.getByTestId("risposta").last();
    await expect(answer).toContainText(el.key);
    await answer.getByRole("button", { name: "Aggiungi alla wiki" }).click();
    await answer.getByRole("button", { name: "Aggiungi", exact: true }).click();
    await expect(answer.getByRole("status")).toContainText("Aggiunto a «Appunti».");
    await page.keyboard.press("Escape");

    await page.goto("/profilo/wiki");
    const ai = page.getByTestId("wiki-assistente");
    await expect(ai).toBeVisible();
    await ai.getByLabel("Lascia che l'assistente tenga aggiornata la wiki").check();
    await expect(ai.getByLabel("Lascia che l'assistente tenga aggiornata la wiki")).toBeChecked();
    await ai.getByRole("button", { name: "Aggiorna adesso" }).click();
    await expect(ai.getByTestId("wiki-stato")).toContainText(/Ultimo aggiornamento .*: 1 pagina cambiata\./, { timeout: 20_000 });
    const pages = page.getByRole("list", { name: "Pagine" });
    const prio = pages.getByRole("listitem").filter({ hasText: "Le mie priorità" });
    await expect(prio).toContainText("assistente");
    await expect(prio).toContainText(el.key);

    const log = page.getByRole("list", {
      name: "Modifiche recenti della wiki",
    });
    await expect(log.getByRole("listitem").first()).toContainText("Le mie priorità");
    await log
      .getByRole("button", {
        name: "Annulla la modifica 1 di «Le mie priorità»",
      })
      .click();
    await expect(page.getByTestId("wiki-registro").getByRole("status")).toHaveText("Modifica annullata.");
    await expect(pages.getByRole("listitem").filter({ hasText: "Le mie priorità" })).toHaveCount(0);
    const usage = await call<{ by_feature: { key: string; calls: number }[] }>(page.request, "GET", "/api/v1/ai/usage");
    expect(usage.by_feature.find((x) => x.key === "wiki")?.calls).toBeGreaterThanOrEqual(1);
  });

  test("a 375 px: niente scorrimento orizzontale, axe e target tattili", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    await login(page, "admin", TENANT);
    await page.goto("/profilo/wiki");
    await expect(page.getByTestId("wiki-assistente")).toBeVisible();
    await expect(page.getByRole("list", { name: "Modifiche recenti della wiki" })).toBeVisible();
    await expectNoHorizontalScroll(page);
    expect(await seriousA11y(page)).toEqual([]);
    await touchTargets(page, "wiki");

    await page.getByRole("list", { name: "Pagine" }).getByRole("link", { name: "Comitato IT" }).click();
    const box = page.getByTestId("wiki-pagina");
    await box.getByRole("button", { name: "Storia" }).click();
    await box.getByRole("list", { name: "Versioni della pagina" }).getByRole("listitem").first().getByRole("button", { name: "Differenze" }).click();
    await expect(box.getByRole("region", { name: /Differenze della versione/ })).toBeVisible();
    await expectNoHorizontalScroll(page);
    expect(await seriousA11y(page)).toEqual([]);
    // I link dentro il testo della pagina sono in linea (eccezione di WCAG 2.5.8): si misurano azioni e storia.
    await touchTargets(page, "wiki-azioni");
    await touchTargets(page, "wiki-storia");
  });
});
