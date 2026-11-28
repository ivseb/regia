// Questionari precompilati dall'AI (funzione «questionari»): con l'AI spenta la valutazione è
// identica (si vedono solo le domande da guardare con attenzione); con l'AI accesa «Proponi le
// risposte» mette sotto ogni domanda la proposta con la fonte e la confidenza, o «nessuna fonte»;
// accettata, la risposta resta «proposta AI accettata da …». Anche a 375 px, con axe. Fornitore
// finto, in un'organizzazione creata apposta: niente cambia per gli altri test.
import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import { createRequire } from "node:module";
import { apiAs, call, createTenant, expectNoHorizontalScroll, login } from "./fixtures";

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
let REVIEW = "";

async function setAI(req: APIRequestContext, on: boolean) {
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: false });
  if (!on) return;
  const v = await call<{ providers: { id: string }[] }>(req, "GET", "/api/v1/ai/settings");
  for (const p of v.providers) await call(req, "DELETE", `/api/v1/ai/providers/${p.id}`);
  const p = await call<{ id: string }>(req, "POST", "/api/v1/ai/providers", { kind: "fake", name: "Finto e2e" });
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: true, deep: { provider_id: p.id, model: "finto-approfondito" } });
}

test.describe("Questionari precompilati dall'AI", () => {
  test.describe.configure({ mode: "serial" });

  test.beforeAll(async ({ playwright, baseURL }) => {
    const { req: platform } = await apiAs(playwright, baseURL!, "admin");
    const slug = `aiq-${Date.now().toString(36)}${Math.floor(Math.random() * 1e4).toString(36)}`;
    await createTenant(platform, "Org questionari AI " + slug, slug);
    await platform.dispose();
    TENANT = slug;
    const { req } = await apiAs(playwright, baseURL!, "admin", slug);
    await call(req, "PUT", "/api/v1/tenant/modules", { modules: { architecture: true } });
    await call(req, "POST", "/api/v1/architecture/applications/import", {
      csv: "codice;nome;criticita;funzione_critica;classificazione_dati;hosting\nFE;Frontend area clienti;alta;no;personali;cloud_ue\n",
    });
    const apps = await call<{ items: { id: string; code: string }[] }>(req, "GET", "/api/v1/architecture/applications");
    const fe = apps.items.find((a) => a.code === "FE")!;
    const br = await call<{ id: string; key: string }>(req, "POST", "/api/v1/demand/requests", { title: "Area clienti rinnovata" });
    const imp = await call<{ key: string }>(req, "POST", `/api/v1/demand/requests/${br.key}/impacts`, { application_id: fe.id, estimate_effort_days: 5 });
    const opened = await call<{ review: { key: string } }>(req, "POST", `/api/v1/elements/${br.id}/reviews`, { type: "design_review", impact: imp.key });
    REVIEW = opened.review.key;
    await setAI(req, false);
    await req.dispose();
  });

  test("senza AI la valutazione è identica, con le domande da guardare evidenziate", async ({ page }) => {
    await login(page, "admin", TENANT);
    await page.goto(`/valutazioni/${REVIEW}`);
    const hosting = page.locator("#domanda-hosting");
    await expect(hosting.getByText("Genera prescrizioni o requisiti")).toBeVisible();
    await expect(page.getByRole("button", { name: "Proponi le risposte" })).toHaveCount(0);
    await expect(page.getByTestId("proposta-ai")).toHaveCount(0);
  });

  test("con l'AI le proposte hanno fonte e confidenza, e si accettano", async ({ page, playwright, baseURL }) => {
    const { req } = await apiAs(playwright, baseURL!, "admin", TENANT);
    await setAI(req, true);
    await req.dispose();
    await login(page, "admin", TENANT);
    await page.goto(`/valutazioni/${REVIEW}`);
    const section = page.getByRole("region", { name: "Soluzione", exact: true });
    await section.getByRole("button", { name: "Proponi le risposte" }).click();
    await expect(section.getByText(/proposte con fonte/)).toBeVisible();

    // L'hosting viene dal fatto dell'applicazione: confidenza al massimo media, con il link.
    const hosting = page.locator("#domanda-hosting");
    const box = hosting.getByTestId("proposta-ai");
    await expect(box.getByText("confidenza media")).toBeVisible();
    await expect(box.getByRole("link", { name: /Hosting: cloud_ue/ })).toBeVisible();
    await expect(box.getByText("Guardala con attenzione prima di accettarla.")).toBeVisible();
    // La descrizione non ha fonte: da compilare a mano.
    await expect(page.locator("#domanda-descrizione").getByText("Nessuna fonte: da compilare a mano.")).toBeVisible();

    await box.getByRole("button", { name: "Accetta" }).click();
    await expect(hosting.getByText(/Proposta AI accettata da/)).toBeVisible();
    await expect(hosting.getByRole("radio", { name: "Cloud aziendale (UE)" })).toBeChecked();
    await expect(hosting.getByTestId("proposta-ai")).toHaveCount(0);
  });

  test("a 375 px: niente scorrimento orizzontale e nessun problema serio di accessibilità", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 800 });
    await login(page, "admin", TENANT);
    await page.goto(`/valutazioni/${REVIEW}`);
    const section = page.getByRole("region", { name: "Soluzione", exact: true });
    await section.getByRole("button", { name: "Proponi le risposte" }).click();
    await expect(page.locator("#domanda-descrizione").getByTestId("proposta-ai")).toBeVisible();
    await expectNoHorizontalScroll(page);
    expect(await seriousA11y(page)).toEqual([]);
  });
});
