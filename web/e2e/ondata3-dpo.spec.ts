// Ondata 3, Vista DPO (replica/usabilita/governo.md, C8): chi ha il ruolo del DPO vede la voce «Privacy (DPO)»
// e la home; registra un trattamento con la base giuridica e le applicazioni, avvia e porta in revisione la
// DPIA, vede la violazione con le 72 ore e registra la notifica, esporta il registro in CSV; chi non ha il
// permesso non vede la voce; telefono a 375 px senza scorrimento orizzontale e con bersagli da 44 px.
import { test, expect } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, scegli, uniq } from "./fixtures";

test("DPO: home, trattamento, DPIA, violazione con 72 ore, CSV e telefono", async ({ page, playwright, baseURL }) => {
  const stamp = Date.now().toString(36).toUpperCase();
  const code = `DPO${stamp}`.slice(0, 14);
  const appName = `Anagrafica ${stamp}`;
  const { req: admin } = await apiAs(playwright, baseURL!, "admin");
  const csv = `codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\n${code};${appName};media;no;personali;;\n`;
  await call(admin, "POST", "/api/v1/architecture/applications/import", { csv });
  const incTitle = uniq("Esposizione anagrafica");
  await call(admin, "POST", "/api/v1/incidents", {
    title: incTitle, description: "Creato dai test end-to-end.", detected_at: new Date(Date.now() - 2 * 3_600_000).toISOString(), impacted: [code],
  });

  await login(page, "dpo");
  await page.goto("/");
  // «Privacy (DPO)» è una scheda della voce «Rischi e conformità» (menu snello).
  await page.getByRole("navigation", { name: "Navigazione principale" }).getByRole("link", { name: "Rischi e conformità", exact: true }).click();
  const grc = page.getByRole("navigation", { name: "Rischi e conformità" });
  await expect(grc.getByRole("link", { name: "Privacy (DPO)" })).toBeVisible();
  await grc.getByRole("link", { name: "Privacy (DPO)" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Privacy e protezione dei dati" })).toBeVisible();

  // Home: l'applicazione con dati personali senza trattamento e la violazione da notificare.
  await expect(page.getByRole("heading", { name: /Applicazioni con dati personali senza trattamento/ }).first()).toBeVisible();
  await expect(page.getByRole("link", { name: appName }).first()).toBeVisible();
  await expect(page.getByText(incTitle).first()).toBeVisible();
  await expect(page.getByRole("timer").first()).toContainText(/mancano|scaduta/);

  // Registra un trattamento dall'applicazione senza trattamento: parte con l'applicazione già scelta.
  const row = page.getByRole("listitem").filter({ hasText: appName }).first();
  await row.getByRole("button", { name: "Crea il trattamento" }).click();
  const dlg = page.getByRole("dialog");
  const name = uniq("Gestione anagrafica");
  await dlg.getByLabel("Nome del trattamento").fill(name);
  await dlg.getByLabel("Finalità").fill("Gestire le anagrafiche dei clienti.");
  await dlg.getByLabel("Categorie di interessati").fill("Clienti");
  await dlg.getByLabel("Categorie particolari di dati (salute, biometrici, …)").check();
  await scegli(dlg.getByRole("combobox", { name: "Base giuridica" }), "Esecuzione di un contratto");
  await dlg.getByRole("button", { name: "Salva", exact: true }).click();
  await expect(dlg).toHaveCount(0);

  // Il registro lo mostra, con la DPIA necessaria; l'applicazione non è più senza trattamento.
  await page.getByRole("tab", { name: "Trattamenti" }).click();
  const item = page.getByRole("listitem").filter({ hasText: name });
  await expect(item).toBeVisible();
  await expect(item.getByText("DPIA mancante")).toBeVisible();
  await expect(item.getByText(appName)).toBeVisible();

  // DPIA: descrizione e valutazione dei rischi, poi in revisione.
  await item.getByRole("button", { name: "Avvia la DPIA" }).click();
  const dpia = page.getByRole("dialog");
  await dpia.getByLabel("Descrizione del trattamento").fill("Trattamento su larga scala delle anagrafiche.");
  await dpia.getByLabel("Rischi per gli interessati").fill("Accesso non autorizzato e perdita di riservatezza.");
  await dpia.getByRole("button", { name: "Salva", exact: true }).click();
  await expect(dpia.getByRole("button", { name: "Metti in revisione" })).toBeVisible();
  await dpia.getByRole("button", { name: "Metti in revisione" }).click();
  await expect(dpia.getByText("In revisione").first()).toBeVisible();
  await dpia.getByRole("button", { name: "Chiudi", exact: true }).last().click();
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await page.getByRole("tab", { name: "DPIA" }).click();
  await expect(page.getByRole("listitem").filter({ hasText: name }).getByText("In revisione")).toBeVisible();

  // Violazioni: l'incidente tocca un'applicazione con dati personali, con le 72 ore ben in vista.
  await page.getByRole("tab", { name: "Violazioni" }).click();
  const breach = page.getByRole("listitem").filter({ hasText: incTitle });
  await expect(breach).toBeVisible();
  await expect(breach.getByText("Notifica entro 72 ore")).toBeVisible();
  await expect(breach.getByRole("timer")).toContainText("mancano");
  await breach.getByRole("button", { name: "Registra la notifica" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Salva", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("listitem").filter({ hasText: incTitle }).getByText(/Notificata il .*nei termini/)).toBeVisible();

  // Flussi: il trattamento con la sua applicazione.
  await page.getByRole("tab", { name: "Flussi di dati" }).click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
  await expect(page.getByRole("link", { name: appName }).first()).toBeVisible();

  // CSV del registro.
  await page.getByRole("tab", { name: "Trattamenti" }).click();
  const [dl] = await Promise.all([page.waitForEvent("download"), page.getByRole("link", { name: "Esporta il CSV" }).click()]);
  expect(dl.suggestedFilename()).toMatch(/^registro-trattamenti-.*\.csv$/);
  const { readFileSync } = await import("node:fs");
  const body = readFileSync((await dl.path())!, "utf8");
  expect(body).toContain(name);
  expect(body).toContain("Esecuzione di un contratto");
  expect(body).toContain(appName);
  await admin.dispose();
});

test("DPO a 375 px: nessuno scorrimento orizzontale e bersagli da 44 px", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "dpo");
  for (const scheda of ["", "?scheda=trattamenti", "?scheda=dpia", "?scheda=violazioni", "?scheda=flussi"]) {
    await page.goto(`/privacy${scheda}`);
    await expect(page.getByRole("heading", { level: 1, name: "Privacy e protezione dei dati" })).toBeVisible();
    await expect(page.getByRole("tablist")).toBeVisible();
    await expectNoHorizontalScroll(page);
    for (const tab of await page.getByRole("tab").all()) {
      expect((await tab.boundingBox())!.height, "altezza della scheda").toBeGreaterThanOrEqual(44);
    }
  }
  await page.goto("/privacy?scheda=trattamenti");
  await page.getByRole("button", { name: "Nuovo trattamento" }).click();
  const dlg = page.getByRole("dialog");
  await expect(dlg).toBeVisible();
  await expectNoHorizontalScroll(page);
  for (const b of await dlg.getByRole("button", { name: "Salva", exact: true }).all()) {
    expect((await b.boundingBox())!.height, "altezza del bersaglio").toBeGreaterThanOrEqual(44);
  }
});

test("la voce di menu e le API privacy sono solo per chi ha il permesso", async ({ page, playwright, baseURL }) => {
  await login(page, "dm");
  await page.goto("/rischi");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect(page.getByRole("link", { name: "Privacy (DPO)" })).toHaveCount(0);
  const { req } = await apiAs(playwright, baseURL!, "dm");
  expect((await req.get("/api/v1/privacy/home")).status()).toBe(403);
  await req.dispose();
  const { req: admin } = await apiAs(playwright, baseURL!, "admin");
  expect((await admin.get("/api/v1/privacy/home")).status()).toBe(200);
  await admin.dispose();
});
