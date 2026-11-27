// Ondata 3, proposta 4: controlli GRC vivi. L'efficacia la propone il sistema (regola sui dati del
// grafo, con il motivo), la conferma una persona con la nota; evidenze automatiche; i controlli di
// trattamento nella scheda del rischio; mappatura guidata requisito ↔ controllo in blocco; telefono.
import { test, expect, type APIRequestContext } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, uniq } from "./fixtures";

type Ctl = { key: string };

/** Controllo con una regola che oggi è sempre vera («efficace»): i fatti del grafo dipendono dai dati demo. */
async function controlWithRule(req: APIRequestContext, title: string, rule: Record<string, unknown> = {}) {
  const c = await call<Ctl>(req, "POST", "/api/v1/grc/controls", { title, text: "Controllo creato dai test end-to-end: patch e vulnerabilità." });
  await call(req, "PUT", `/api/v1/grc/controls/${c.key}/proposal/rule`, {
    name: "Regola dei test", effective_when: "rilasci_senza_sbom: >=0", at_risk_when: "", sources: ["release", "gate", "sbom", "rule"], active: true, ...rule,
  });
  return c.key;
}

test("proposta del sistema: motivo e dati visibili, non conferma con nota obbligatoria, conferma con nota", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "conformita");
  const declined = await controlWithRule(req, uniq("Patch e vulnerabilità A"));
  const confirmed = await controlWithRule(req, uniq("Patch e vulnerabilità B"));
  // La sincronizzazione legge rilasci, gate e SBOM del grafo e registra la verifica della regola.
  const sync = await call<{ controls: number }>(req, "POST", "/api/v1/grc/living/sync", {});
  expect(sync.controls).toBeGreaterThanOrEqual(2);

  await login(page, "conformita");
  await page.goto(`/e/${declined}`);
  await expect(page.getByRole("tab", { name: "Conformità" })).toHaveAttribute("aria-selected", "true");
  const box = page.getByRole("region", { name: "Proposta del sistema" });
  await expect(box).toBeVisible();
  await expect(box.getByTestId("proposal-state")).toContainText("efficace");
  await expect(box.getByTestId("proposal-reason")).toContainText("«efficace se» è soddisfatta");
  // Il dato che ha deciso la proposta, con il collegamento.
  await expect(box.getByRole("link", { name: "Rilasci in produzione senza SBOM negli ultimi 90 giorni" })).toBeVisible();

  // Non confermare: la nota è obbligatoria e lo stato ufficiale non cambia.
  await box.getByRole("button", { name: "Non confermare" }).click();
  const send = box.getByRole("button", { name: "Registra la decisione" });
  await expect(send).toBeDisabled();
  await box.getByLabel("Nota").fill("Aspetto la scansione di fine mese prima di confermare.");
  await send.click();
  await expect(box.getByText("Hai già deciso di non confermare questa proposta")).toBeVisible();
  const c1 = await call<{ effectiveness: string }>(req, "GET", `/api/v1/grc/controls/${declined}`);
  expect(c1.effectiveness).toBe("not_assessed");

  // Conferma con la nota: cambia lo stato ufficiale.
  await page.goto(`/e/${confirmed}`);
  const box2 = page.getByRole("region", { name: "Proposta del sistema" });
  await box2.getByRole("button", { name: "Conferma", exact: true }).click();
  await box2.getByLabel("Nota").fill("Verificato con la sicurezza.");
  await box2.getByRole("button", { name: "Registra la decisione" }).click();
  await expect(box2.getByText("Lo stato ufficiale coincide con la proposta.")).toBeVisible();
  const c2 = await call<{ effectiveness: string; evidence: { title: string }[] }>(req, "GET", `/api/v1/grc/controls/${confirmed}`);
  expect(c2.effectiveness).toBe("effective");
  // Evidenza automatica dalla regola, con data e validità.
  expect(c2.evidence.some((e) => e.title.startsWith("Verifica automatica"))).toBe(true);
  await expect(page.getByText(/Verifica automatica/).first()).toBeVisible();
});

test("rischio: i controlli di trattamento collegati con efficacia ed evidenze", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "conformita");
  const key = await controlWithRule(req, uniq("Controllo di trattamento"));
  const risks = await apiAs(playwright, baseURL!, "rischi");
  const risk = await call<{ key: string }>(risks.req, "POST", "/api/v1/risks", {
    title: uniq("Rischio con trattamento"), category: "Sicurezza", inherent_likelihood: 3, inherent_impact: 4,
  });
  await call(risks.req, "POST", `/api/v1/risks/${risk.key}/links`, { target: key });
  const view = await call<{ controls: { key: string }[] }>(req, "GET", `/api/v1/grc/risks/${risk.key}/controls`);
  expect(view.controls.map((c) => c.key)).toContain(key);

  await login(page, "conformita");
  await page.goto(`/e/${risk.key}?scheda=dettagli`);
  const panel = page.getByRole("region", { name: "Controlli di trattamento" });
  await expect(panel.getByRole("link", { name: new RegExp(key) })).toBeVisible();
  await expect(panel.getByText("Efficacia:")).toBeVisible();
  await expect(panel.getByText(/proposta in attesa: efficace/)).toBeVisible();
});

test("mappatura guidata: scelgo i requisiti, il controllo e li associo in blocco", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "conformita");
  const title = uniq("Resilienza e continuità operativa");
  const c = await call<Ctl>(req, "POST", "/api/v1/grc/controls", { title, text: "Piani di continuità e ripristino delle funzioni critiche." });
  await login(page, "conformita");
  await page.goto("/conformita?tab=mappatura");
  await expect(page.getByRole("heading", { name: "Mappatura guidata" })).toBeVisible();
  const left = page.getByTestId("map-left");
  const right = page.getByTestId("map-right");
  await expect(left.getByText("Requisiti senza controllo")).toBeVisible();
  const boxes = left.getByRole("checkbox");
  await expect(boxes.first()).toBeVisible();
  await boxes.nth(0).check();
  await boxes.nth(1).check();
  await expect(right.getByText("2 requisiti scelti").first()).toBeVisible();
  // Il controllo appena creato è tra tutti i controlli; lo cerco e lo scelgo.
  await right.getByRole("searchbox", { name: "Cerca un controllo" }).fill(title);
  await right.getByRole("radio", { name: new RegExp(title) }).first().check();
  await right.getByRole("button", { name: /^Associa 2 requisiti a / }).click();
  await expect(page.getByTestId("map-done")).toContainText("2 requisiti associati");
  const after = await call<{ requirements: unknown[] }>(req, "GET", `/api/v1/grc/controls/${c.key}`);
  expect(after.requirements.length).toBe(2);
});

test("telefono a 375 px: proposta e mappatura senza scorrimento orizzontale, una colonna alla volta", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "conformita");
  const key = await controlWithRule(req, uniq("Controllo da telefono"));
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "conformita");
  await page.goto(`/e/${key}`);
  const box = page.getByRole("region", { name: "Proposta del sistema" });
  await expect(box).toBeVisible();
  for (const name of ["Conferma", "Non confermare"]) {
    const b = box.getByRole("button", { name, exact: true });
    await expect(b).toBeVisible();
    expect((await b.boundingBox())!.height).toBeGreaterThanOrEqual(43.5);
  }
  await expectNoHorizontalScroll(page);

  await page.goto("/conformita?tab=mappatura");
  const left = page.getByTestId("map-left");
  const right = page.getByTestId("map-right");
  await expect(left).toBeVisible();
  await expect(right).toBeHidden();
  await left.getByRole("checkbox").first().check();
  await expectNoHorizontalScroll(page);
  const next = page.getByRole("button", { name: "Avanti: scegli il controllo" });
  expect((await next.boundingBox())!.height).toBeGreaterThanOrEqual(43.5);
  await next.click();
  await expect(right).toBeVisible();
  await expect(left).toBeHidden();
  await expectNoHorizontalScroll(page);
  await page.getByRole("button", { name: "Torna ai requisiti" }).click();
  await expect(left).toBeVisible();
});
