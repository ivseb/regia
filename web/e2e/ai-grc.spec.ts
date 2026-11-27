// AI, ondata 5 (replica/ai.md §2 punto 5): GRC assistito. Collegamento evidenza → controllo con le
// proposte accettate una per una, cosa manca al controllo, bozza di policy salvata come documento
// controllato in bozza, bozza della notifica DORA con i campi del modello ufficiale, salvata e
// esportata. Senza AI schede e notifiche restano identiche. Tutto con il fornitore finto, in
// un'organizzazione creata apposta: niente cambia per gli altri test.
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
  const boxes = await page.getByTestId(scope).first().locator("button:visible, a:visible").evaluateAll((els) =>
    els.map((e) => ({ name: (e.textContent ?? "").trim().slice(0, 40), h: e.getBoundingClientRect().height })),
  );
  for (const b of boxes) expect(b.h, `altezza di «${b.name}»`).toBeGreaterThanOrEqual(43.5);
}

let TENANT = "";
const data = { backup: "", access: "", evidence: "", other: "", incident: "", notice: "" };

async function setAI(req: APIRequestContext, on: boolean) {
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: false });
  if (!on) return;
  const v = await call<{ providers: { id: string }[] }>(req, "GET", "/api/v1/ai/settings");
  for (const p of v.providers) await call(req, "DELETE", `/api/v1/ai/providers/${p.id}`);
  const p = await call<{ id: string }>(req, "POST", "/api/v1/ai/providers", { kind: "fake", name: "Finto e2e" });
  await call(req, "PUT", "/api/v1/ai/settings", { enabled: true, deep: { provider_id: p.id, model: "finto-approfondito" } });
}

test.describe("AI, ondata 5: GRC assistito", () => {
  test.describe.configure({ mode: "serial" });

  test.beforeAll(async ({ playwright, baseURL }) => {
    const { req: platform } = await apiAs(playwright, baseURL!, "admin");
    const slug = `ai5-${Date.now().toString(36)}${Math.floor(Math.random() * 1e4).toString(36)}`;
    await createTenant(platform, uniq("Org AI 5"), slug);
    await platform.dispose();
    TENANT = slug;
    const { req } = await apiAs(playwright, baseURL!, "admin", slug);
    await call(req, "PUT", "/api/v1/tenant/modules", { modules: { grc: true, knowledge: true, architecture: true } });
    await call(req, "POST", "/api/v1/grc/starters/dora", { enable: true });
    const bk = await call<{ key: string }>(req, "POST", "/api/v1/grc/controls", {
      title: "Backup cifrati e prove di ripristino",
      text: "Backup giornalieri cifrati e prova di ripristino trimestrale.",
      frequency: "quarterly",
      requirements: [{ ref: "dora:Art. 12" }],
    });
    const ac = await call<{ key: string }>(req, "POST", "/api/v1/grc/controls", {
      title: "Revisione degli accessi privilegiati",
      text: "Revisione semestrale delle utenze amministrative.",
      frequency: "semiannual",
      requirements: [{ ref: "dora:Art. 9" }],
    });
    const ev = await call<{ key: string }>(req, "POST", "/api/v1/grc/evidence", {
      controls: [ac.key],
      kind: "note",
      title: "Rapporto del test di ripristino dei backup cifrati",
      note: "Prova di ripristino trimestrale riuscita sui backup cifrati.",
    });
    const other = await call<{ key: string }>(req, "POST", "/api/v1/grc/evidence", {
      controls: [ac.key],
      kind: "note",
      title: "Verbale dei backup cifrati del trimestre",
      note: "Esito dei backup cifrati e della prova di ripristino.",
    });
    await call(req, "POST", "/api/v1/architecture/applications/import", {
      csv: "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\nPAG;Pagamenti online;critica;sì;personali;;Canali digitali\n",
    });
    const apps = await call<{ items: { code: string; key: string }[] }>(req, "GET", "/api/v1/architecture/applications");
    const inc = await call<{ key: string; element: { id: string } }>(req, "POST", "/api/v1/incidents", {
      title: "Pagamenti online non disponibili",
      description: "Allarme dei sistemi di monitoraggio sul gateway.",
      detected_at: new Date(Date.now() - 3 * 3600_000).toISOString().replace(/\.\d+Z$/, "Z"),
      clients_affected: 150000,
      downtime_minutes: 180,
      impacted: [apps.items.find((a) => a.code === "PAG")!.key],
    });
    await call(req, "POST", `/api/v1/elements/${inc.element.id}/transitions`, { to: "Classificato" });
    const view = await call<{ notifications: { id: string; scheme_key: string; stage_key: string }[] }>(req, "GET", `/api/v1/incidents/${inc.key}`);
    data.backup = bk.key;
    data.access = ac.key;
    data.evidence = ev.key;
    data.other = other.key;
    data.incident = inc.key;
    data.notice = view.notifications.find((n) => n.scheme_key === "dora" && n.stage_key === "iniziale")!.id;
    await req.dispose();
  });

  test("senza AI le schede del GRC e le notifiche non cambiano", async ({ page }) => {
    await login(page, "admin", TENANT);
    await setAI(page.request, false);
    await page.goto(`/e/${data.evidence}?scheda=dettagli`);
    await expect(page.getByRole("heading", { name: "Evidenza" })).toBeVisible();
    await expect(page.getByTestId("assistente-grc")).toHaveCount(0);
    await page.goto(`/e/${data.backup}?scheda=dettagli`);
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await expect(page.getByTestId("assistente-grc")).toHaveCount(0);
    await page.goto(`/e/${data.incident}?scheda=dettagli`);
    await page.getByRole("region", { name: "Notifiche normative" }).getByRole("button", { name: "prepara" }).first().click();
    await expect(page.getByLabel("Contenuto")).toBeVisible();
    await expect(page.getByTestId("assistente-grc")).toHaveCount(0);
    await expect(page.getByTestId("versioni-notifica")).toHaveCount(0);
  });

  test("dall'evidenza il controllo proposto si collega solo con il clic; dal controllo le candidate e cosa manca", async ({ page }) => {
    await login(page, "admin", TENANT);
    await setAI(page.request, true);
    await page.goto(`/e/${data.evidence}?scheda=dettagli`);
    const box = page.getByTestId("assistente-grc");
    await box.getByRole("button", { name: "Proponi i controlli che soddisfa" }).click();
    const props = box.getByRole("list", { name: "Proposte di collegamento" });
    await expect(props.getByRole("link", { name: data.backup })).toBeVisible();
    await expect(props).toContainText("copertura piena");
    // Nulla si collega finché la persona non lo chiede.
    let ev = await call<{ linked_controls: { key: string }[] }>(page.request, "GET", `/api/v1/grc/evidence/${data.evidence}`);
    expect(ev.linked_controls.map((c) => c.key)).not.toContain(data.backup);
    await props.getByRole("button", { name: `Collega ${data.backup}` }).click();
    await expect(box.getByRole("status").filter({ hasText: "Collegata" })).toBeVisible();
    ev = await call(page.request, "GET", `/api/v1/grc/evidence/${data.evidence}`);
    expect(ev.linked_controls.map((c) => c.key)).toContain(data.backup);

    await page.goto(`/e/${data.backup}?scheda=dettagli`);
    const cbox = page.getByTestId("assistente-grc");
    await cbox.getByRole("button", { name: "Cerca le evidenze e cosa manca" }).click();
    await expect(cbox.getByRole("list", { name: "Proposte di collegamento" }).getByRole("link", { name: data.other })).toBeVisible();
    await expect(cbox.getByTestId("mancanze-grc")).toContainText("dora:Art. 12");
    await cbox.getByRole("button", { name: "Scarta" }).click();
    await expect(cbox.getByRole("status").filter({ hasText: "Scartata" })).toBeVisible();
  });

  test("bozza di policy dal controllo, salvata come documento controllato in bozza", async ({ page }) => {
    await login(page, "admin", TENANT);
    await setAI(page.request, true);
    const space = `P${Date.now().toString(36).slice(-4).toUpperCase()}`;
    await call(page.request, "POST", "/api/v1/kb/spaces", { key: space, name: uniq("Policy") });
    await page.goto(`/e/${data.backup}?scheda=dettagli`);
    const box = page.getByTestId("assistente-grc");
    await box.getByRole("button", { name: "Scrivi una bozza di policy o runbook" }).click();
    await box.getByRole("radio", { name: "Policy" }).check();
    await box.getByRole("button", { name: "Scrivi la bozza" }).click();
    const text = box.getByLabel("Bozza in Markdown");
    await expect(text).toHaveValue(/## Scopo[\s\S]*dora:Art\. 12/);
    await expect(box.getByLabel("Titolo")).toHaveValue("Policy: Backup cifrati e prove di ripristino");
    await text.fill("## Scopo\nRivista dalla compliance: backup e ripristino.\n\n## Riferimenti\n- " + data.backup);
    await box.getByRole("button", { name: "Salva come bozza in Knowledge" }).click();
    const saved = box.getByRole("status").filter({ hasText: "Salvata come documento controllato" });
    await expect(saved).toContainText("Bozza");
    const key = (await saved.getByRole("link").textContent())!.trim();
    const pg = await call<{ page: { body: string; controlled: boolean; approved_version: number | null } }>(page.request, "GET", `/api/v1/kb/pages/${key}`);
    expect(pg.page.body).toContain("Rivista dalla compliance");
    expect(pg.page.controlled).toBe(true);
    expect(pg.page.approved_version).toBeNull();
  });

  test("bozza della notifica DORA: campi dal modello ufficiale, salvata nell'incidente ed esportata", async ({ page }) => {
    await login(page, "admin", TENANT);
    await setAI(page.request, true);
    await page.goto(`/e/${data.incident}?scheda=dettagli`);
    await page.getByRole("region", { name: "Notifiche normative" }).getByRole("button", { name: "prepara" }).first().click();
    const box = page.getByTestId("assistente-grc");
    await box.getByRole("button", { name: "Prepara la bozza DORA" }).click();
    const draft = box.getByTestId("bozza-dora");
    await expect(draft.getByRole("status")).toContainText("25 campi");
    await expect(draft.getByLabel(/^2\.1 /)).toHaveValue(data.incident);
    await expect(draft.getByLabel(/^2\.4 /)).toHaveValue(/Pagamenti online non disponibili/);
    await expect(draft.getByTestId("campo-1.3")).toContainText("mancante");
    await draft.getByLabel(/^1\.3 /).fill("5299000000000000AB12");
    await expect(draft.getByTestId("campo-1.3")).toContainText("da verificare");
    await draft.getByRole("button", { name: "Salva come bozza nell'incidente" }).click();
    await expect(draft.getByRole("status").filter({ hasText: "Salvata come versione 1" })).toBeVisible();
    await expect(page.getByLabel("Contenuto")).toHaveValue(/5299000000000000AB12/);
    const versions = box.getByTestId("versioni-notifica");
    await versions.locator("summary").click();
    await expect(versions.getByRole("link", { name: "Markdown" })).toHaveAttribute("href", /drafts\/1\/export\?format=md/);
    const md = await page.request.get(`/api/v1/regulatory-notifications/${data.notice}/drafts/1/export?format=md`);
    expect(md.ok()).toBeTruthy();
    expect(await md.text()).toContain("**2.1 ");
    const usage = await call<{ by_feature: { key: string; calls: number }[] }>(page.request, "GET", "/api/v1/ai/usage");
    expect(usage.by_feature.find((x) => x.key === "grc")?.calls).toBeGreaterThanOrEqual(1);
  });

  test("a 375 px e con axe: proposte dell'evidenza e bozza DORA", async ({ page }) => {
    await login(page, "admin", TENANT);
    await setAI(page.request, true);
    await page.goto(`/e/${data.other}?scheda=dettagli`);
    const box = page.getByTestId("assistente-grc");
    await box.getByRole("button", { name: "Proponi i controlli che soddisfa" }).click();
    await expect(box.getByTestId("risposta-grc")).toBeVisible();
    await page.waitForLoadState("networkidle");
    expect(await seriousA11y(page)).toEqual([]);
    await page.setViewportSize({ width: 375, height: 812 });
    await expectNoHorizontalScroll(page);
    await touchTargets(page, "assistente-grc");

    await page.goto(`/e/${data.incident}?scheda=dettagli`);
    await page.getByRole("region", { name: "Notifiche normative" }).getByRole("button", { name: "prepara" }).first().click();
    const nbox = page.getByTestId("assistente-grc");
    await nbox.getByRole("button", { name: /Prepara (la bozza DORA|di nuovo)/ }).click();
    await expect(nbox.getByTestId("bozza-dora")).toBeVisible();
    await expectNoHorizontalScroll(page);
    await touchTargets(page, "assistente-grc");
    await page.setViewportSize({ width: 1400, height: 900 });
    expect(await seriousA11y(page)).toEqual([]);
  });
});
