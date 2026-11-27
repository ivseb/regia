// AI, ondata 6 (replica/ai.md §3, §4): fornitori nativi nelle impostazioni. Bedrock e Vertex si
// configurano con regione e credenziali, che non tornano mai indietro; la prova di connessione
// (con il fornitore finto: nessuna chiamata a servizi veri, nessuna chiave vera) elenca i modelli
// e segna quello senza strumenti, e il livello lo dice. Lavora in un'organizzazione creata
// apposta, come gli altri e2e dell'AI.
import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import { createRequire } from "node:module";
import { apiAs, call, createTenant, expectNoHorizontalScroll, login, scegli, uniq } from "./fixtures";

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

async function touchTargets(page: Page, scope: ReturnType<Page["getByRole"]>) {
  const boxes = await scope.locator("button:visible, a:visible, input[type=radio]:visible").evaluateAll((els) =>
    els.map((e) => {
      const target = e instanceof HTMLInputElement ? (e.closest("label") ?? e) : e;
      return { name: (target.textContent ?? "").trim().slice(0, 40), h: target.getBoundingClientRect().height };
    }),
  );
  for (const b of boxes) expect(b.h, `altezza di «${b.name}»`).toBeGreaterThanOrEqual(43.5);
}

let TENANT = "";

async function resetAI(req: APIRequestContext) {
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: false });
  const v = await call<{ providers: { id: string }[] }>(req, "GET", "/api/v1/ai/settings");
  for (const p of v.providers) await call(req, "DELETE", `/api/v1/ai/providers/${p.id}`);
}

// Una chiave di service account finta: la forma giusta, nessuna chiave vera (non si contatta Google).
const FAKE_SA = JSON.stringify({
  type: "service_account",
  project_id: "regia-e2e-ue",
  private_key: "-----BEGIN PRIVATE KEY-----\nfinta-per-e2e\n-----END PRIVATE KEY-----\n",
  client_email: "regia@regia-e2e-ue.iam.gserviceaccount.com",
  token_uri: "https://oauth2.googleapis.com/token",
});

test.describe("AI: fornitori nativi", () => {
  test.describe.configure({ mode: "serial" });

  test.beforeAll(async ({ playwright, baseURL }) => {
    const { req } = await apiAs(playwright, baseURL!, "admin");
    const slug = `ai6-${Date.now().toString(36)}${Math.floor(Math.random() * 1e4).toString(36)}`;
    await createTenant(req, uniq("Org AI ondata 6"), slug);
    const me = await call<{ tenants: { slug: string }[] }>(req, "GET", "/api/v1/me");
    expect(me.tenants.map((t) => t.slug)).toContain(slug);
    await req.dispose();
    TENANT = slug;
  });

  test("Bedrock e Vertex: regione e credenziali, che non tornano indietro", async ({ page }) => {
    await login(page, "admin", TENANT);
    await resetAI(page.request);
    await page.goto("/configurazione/ai");
    await expect(page.getByRole("heading", { name: "Intelligenza artificiale" })).toBeVisible();

    // Bedrock con le chiavi d'accesso IAM; prima una regione sbagliata.
    await page.getByRole("button", { name: "Aggiungi fornitore" }).click();
    const form = page.getByRole("form", { name: "Aggiungi fornitore" });
    await scegli(form.getByLabel("Tipo"), "Anthropic via AWS Bedrock");
    await expect(form.getByText("eu-south-1 (Milano)")).toBeVisible();
    await form.getByLabel("Nome", { exact: true }).fill("Bedrock Francoforte");
    await form.getByRole("textbox", { name: /^Regione/ }).fill("Francoforte");
    await form.getByLabel("Chiavi d'accesso IAM").check();
    await form.getByLabel("ID della chiave d'accesso").fill("AKIAFINTAPERE2E01");
    await form.getByLabel("Chiave d'accesso segreta").fill("segreta-finta-e2e");
    await form.getByRole("button", { name: "Aggiungi fornitore" }).click();
    await expect(form.getByRole("alert")).toContainText(/regione.*non valida/i);
    await form.getByRole("textbox", { name: /^Regione/ }).fill("eu-central-1");
    await form.getByRole("button", { name: "Aggiungi fornitore" }).click();
    const bedrock = page.getByTestId("fornitore").filter({ hasText: "Bedrock Francoforte" });
    await expect(bedrock).toContainText("Anthropic via AWS Bedrock");
    await expect(bedrock.getByTestId("fornitore-regione")).toHaveText("eu-central-1");
    await expect(bedrock).toContainText("chiave salvata");

    // Vertex con la chiave del service account: il progetto viene dalla chiave.
    await page.getByRole("button", { name: "Aggiungi fornitore" }).click();
    await scegli(form.getByLabel("Tipo"), "Anthropic via Google Vertex AI");
    await form.getByLabel("Nome", { exact: true }).fill("Vertex Milano");
    await form.getByRole("textbox", { name: /^Regione/ }).fill("europe-west8");
    await form.getByLabel("Chiave JSON del service account").fill(FAKE_SA);
    await form.getByRole("button", { name: "Aggiungi fornitore" }).click();
    const vertex = page.getByTestId("fornitore").filter({ hasText: "Vertex Milano" });
    await expect(vertex.getByTestId("fornitore-regione")).toHaveText("regia-e2e-ue · europe-west8");

    // In modifica le credenziali non si vedono: il campo è vuoto e dice che sono salvate.
    await vertex.getByRole("button", { name: "Modifica" }).click();
    const edit = page.getByRole("form", { name: "Modifica Vertex Milano" });
    await expect(edit.getByTestId("vertex-chiave")).toHaveValue("");
    await expect(edit.getByTestId("vertex-chiave")).toHaveAttribute("placeholder", /Credenziali salvate/);
    await edit.getByRole("textbox", { name: /^Regione/ }).fill("eu");
    await edit.getByRole("button", { name: "Salva il fornitore" }).click();
    await expect(page.getByTestId("fornitore").filter({ hasText: "Vertex Milano" }).getByTestId("fornitore-regione")).toHaveText("regia-e2e-ue · eu");

    const view = JSON.stringify(await call(page.request, "GET", "/api/v1/ai/settings"));
    expect(view).not.toContain("segreta-finta-e2e");
    expect(view).not.toContain("PRIVATE KEY");
    expect(await page.content()).not.toContain("segreta-finta-e2e");
  });

  test("la prova di connessione segna il modello senza strumenti e il livello lo dice", async ({ page }) => {
    await login(page, "admin", TENANT);
    await resetAI(page.request);
    await call(page.request, "POST", "/api/v1/ai/providers", { kind: "fake", name: "Finto ondata 6" });
    await page.goto("/configurazione/ai");
    const row = page.getByTestId("fornitore").filter({ hasText: "Finto ondata 6" });
    await row.getByRole("button", { name: "Prova la connessione" }).click();
    const result = row.getByTestId("prova-esito");
    await expect(result).toContainText("Connessione riuscita");
    await expect(result).toContainText("3 modelli disponibili");
    await expect(result).toContainText("Segnati come senza strumenti, perché il servizio lo dice: finto-senza-strumenti");

    const deep = page.getByTestId("livello-approfondito");
    await scegli(deep.getByLabel("Fornitore"), "Finto ondata 6");
    await expect(deep.getByLabel("Modello")).toHaveValue("finto-approfondito");
    await expect(deep.getByTestId("modello-senza-strumenti")).toHaveCount(0);
    await deep.getByLabel("Modello").fill("finto-senza-strumenti");
    await expect(deep.getByTestId("modello-senza-strumenti")).toContainText("non sa usare gli strumenti");
    // Il rapido senza un modello suo propone quello dell'approfondito.
    const fast = page.getByTestId("livello-rapido");
    await scegli(fast.getByLabel("Fornitore"), "Finto ondata 6");
    await expect(fast.getByLabel("Modello")).toHaveValue("finto-rapido");
  });

  test("a 375 px il modulo del cloud non scorre in orizzontale, è accessibile e i target sono grandi", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    await login(page, "admin", TENANT);
    await page.goto("/configurazione/ai");
    await expect(page.getByRole("heading", { name: "Intelligenza artificiale" })).toBeVisible();
    await page.getByRole("button", { name: "Aggiungi fornitore" }).click();
    const form = page.getByRole("form", { name: "Aggiungi fornitore" });
    await scegli(form.getByLabel("Tipo"), "Anthropic via AWS Bedrock");
    await form.getByLabel("Chiavi d'accesso IAM").check();
    await expect(form.getByLabel("Token di sessione (facoltativo)")).toBeVisible();
    await expectNoHorizontalScroll(page);
    await touchTargets(page, form);
    expect(await seriousA11y(page)).toEqual([]);
    await scegli(form.getByLabel("Tipo"), "Anthropic via Google Vertex AI");
    await expect(form.getByLabel("Chiave JSON del service account")).toBeVisible();
    await expectNoHorizontalScroll(page);
    expect(await seriousA11y(page)).toEqual([]);
  });
});
