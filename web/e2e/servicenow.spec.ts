// Connettore ServiceNow (sola lettura) contro il finto ServiceNow di sviluppo
// (regia dev fake-servicenow, dati inventati): il percorso in cinque passi, dalle credenziali
// alla prima sincronizzazione, poi la proposta di incidente di Regia in Rischi e incidenti.
// Le stesse pagine a 375 px non scorrono in orizzontale.
import { test, expect, type Page } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { createConnection } from "node:net";
import { resolve } from "node:path";
import { expectNoHorizontalScroll, login, uniq } from "./fixtures";

const root = resolve(import.meta.dirname, "..", "..");
// Una porta per ogni server di prova (E2E_PORT 8140-8149 → 8190-8199), o E2E_SN_PORT.
const port = Number(process.env.E2E_SN_PORT || 8190 + (Number(process.env.E2E_PORT || 8140) - 8140));
const instance = `http://127.0.0.1:${port}`;
const PATH = "/configurazione/integrazioni/servicenow";

function listening(): Promise<boolean> {
  return new Promise((ok) => {
    const s = createConnection({ host: "127.0.0.1", port }, () => {
      s.destroy();
      ok(true);
    });
    s.on("error", () => ok(false));
  });
}

let fake: ChildProcess | null = null;
let integration = "";

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
  if (await listening()) throw new Error(`la porta ${port} è occupata: usa E2E_SN_PORT`);
  const bin = resolve(root, process.env.E2E_BIN || "bin/regia");
  fake = spawn(bin, ["dev", "fake-servicenow", "--addr", `127.0.0.1:${port}`], { cwd: root, stdio: "ignore" });
  const until = Date.now() + 15_000;
  while (!(await listening())) {
    if (fake.exitCode !== null || Date.now() > until) throw new Error("il finto ServiceNow non è partito");
    await new Promise((r) => setTimeout(r, 150));
  }
});

test.afterAll(async () => {
  if (fake && fake.exitCode === null) {
    fake.kill("SIGTERM");
    await new Promise((r) => fake!.once("exit", r));
  }
});

async function waitSynced(page: Page) {
  await expect(page.getByRole("listitem").filter({ hasText: "a mano" }).getByText("riuscita").first()).toBeVisible({ timeout: 30_000 });
}

test("percorso in cinque passi e proposta di incidente", async ({ page }) => {
  await login(page, "admin");
  // Un'applicazione con il codice del servizio «Ordini online» (u_codice = ORD): nasce la proposta.
  await page.request.post("/api/v1/architecture/applications", { data: { name: "Ordini online", code: "ORD" } });

  await page.goto(PATH);
  await expect(page.getByRole("heading", { name: "ServiceNow", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Nuova integrazione" }).click();
  const name = uniq("ServiceNow finto");
  await page.getByLabel("Nome", { exact: true }).fill(name);
  await page.getByLabel("Istanza di ServiceNow").fill(instance);
  await page.getByLabel("Utente di servizio").fill("regia.lettura");
  await page.getByLabel("Password", { exact: true }).fill("finta-password-sviluppo");
  await page.getByLabel("Campo del codice (facoltativo)").fill("u_codice");
  // Priorità predefinite: 1 e 2.
  await expect(page.getByLabel("1 · Critica")).toBeChecked();
  await expect(page.getByLabel("2 · Alta")).toBeChecked();
  await expect(page.getByLabel("3 · Moderata")).not.toBeChecked();
  await page.getByRole("button", { name: "Crea l'integrazione" }).click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
  integration = new URL(page.url()).searchParams.get("sel") ?? "";
  expect(integration).not.toBe("");
  // La password non torna mai.
  await expect(page.getByText("finta-password-sviluppo")).toHaveCount(0);

  // Passo 2: la prova legge le tabelle una per una.
  await page.getByRole("button", { name: "Prova la connessione" }).click();
  const table = page.getByRole("table");
  await expect(table).toBeVisible();
  await expect(table.getByText("change_request")).toBeVisible();
  await expect(table.getByText("non leggibile")).toHaveCount(0);

  // Passi 3 e 4: scoperta dei servizi con la classe, conferma in blocco della proposta.
  await page.getByRole("link", { name: "Servizi", exact: true }).first().click();
  await page.getByRole("button", { name: "Leggi i servizi della CMDB" }).click();
  const ordini = page.getByRole("listitem").filter({ hasText: "Ordini online" }).filter({ hasText: "classe" });
  await expect(ordini.getByText("cmdb_ci_service_auto")).toBeVisible();
  await expect(ordini.getByText(/Proposta: Ordini online/)).toBeVisible();
  await ordini.getByRole("checkbox", { name: "Scegli Ordini online" }).check();
  await page.getByRole("button", { name: "Conferma le proposte scelte (1)" }).click();
  await expect(ordini.getByText(/Associato a Ordini online/)).toBeVisible();

  // Passo 5: prima sincronizzazione.
  await page.getByRole("tab", { name: "Sincronizzazioni" }).click();
  await page.getByRole("button", { name: "Sincronizza adesso" }).click();
  await waitSynced(page);

  // Incidenti e change letti, con il link al record ServiceNow.
  await page.getByRole("tab", { name: "Incidenti e change" }).click();
  const inc = page.getByRole("link", { name: "INC0010001" });
  await expect(inc).toBeVisible();
  await expect(inc).toHaveAttribute("href", new RegExp(`^${instance}`));
  await expect(page.getByRole("link", { name: "CHG0030001" })).toBeVisible();
  // P3 non si legge.
  await expect(page.getByText("INC0010003")).toHaveCount(0);

  // Proposta da ServiceNow in Rischi e incidenti: il modulo già compilato crea l'incidente di Regia.
  await page.goto("/rischi?tab=incidenti");
  const proposals = page.getByRole("region", { name: "Proposte da ServiceNow" });
  await expect(proposals).toBeVisible();
  await expect(proposals.getByText(/La classificazione DORA e NIS2/)).toBeVisible();
  const carrello = proposals.getByRole("listitem").filter({ hasText: "Carrello vuoto dopo il login" }).filter({ has: page.getByRole("button") });
  await carrello.getByRole("button", { name: "Crea incidente di Regia" }).click();
  await expect(page.getByLabel("Cosa succede")).toHaveValue("Carrello vuoto dopo il login");
  await expect(page.getByText(/Dall'incidente INC0010004 di ServiceNow/)).toBeVisible();
  await page.getByRole("button", { name: "Crea l'incidente di Regia" }).click();
  await expect(page).toHaveURL(/\/e\//);
  await expect(page.getByRole("heading", { name: /Carrello vuoto dopo il login/ })).toBeVisible();
});

test("a 375 px non scorre in orizzontale", async ({ page }) => {
  test.skip(!integration, "serve l'integrazione del test precedente");
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "admin");
  for (const search of [`tab=integrazioni&sel=${integration}`, "tab=servizi", "tab=incidenti", "tab=sincronizzazioni"]) {
    await page.goto(`${PATH}?${search}&integrazione=${integration}`);
    await expect(page.getByRole("heading", { name: "ServiceNow", exact: true })).toBeVisible();
    await page.waitForLoadState("networkidle");
    await expectNoHorizontalScroll(page);
  }
  // La prova da telefono: un elenco al posto della tabella.
  await page.goto(`${PATH}?tab=integrazioni&sel=${integration}`);
  await page.getByRole("button", { name: "Prova la connessione" }).click();
  await expect(page.getByRole("list", { name: "Tabelle lette" })).toBeVisible();
  await expect(page.getByRole("table")).toBeHidden();
  await expectNoHorizontalScroll(page);
  await page.goto("/rischi?tab=incidenti");
  await expect(page.getByRole("region", { name: "Proposte da ServiceNow" })).toBeVisible();
  await expectNoHorizontalScroll(page);
});
