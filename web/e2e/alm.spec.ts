// Connettore ALM / Quality Center (sola lettura) contro il finto ALM di sviluppo
// (regia dev fake-alm, dati inventati): dalle credenziali alla prova di connessione, la
// conferma della proposta di un'unità, la prima sincronizzazione e i test set letti del change.
// Le stesse pagine a 375 px non scorrono in orizzontale.
import { test, expect, type Page } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { createConnection } from "node:net";
import { resolve } from "node:path";
import { expectNoHorizontalScroll, login, uniq } from "./fixtures";

const root = resolve(import.meta.dirname, "..", "..");
// Una porta per ogni server di prova (E2E_PORT 8140-8149 → 8200-8209), o E2E_ALM_PORT.
const port = Number(process.env.E2E_ALM_PORT || 8200 + (Number(process.env.E2E_PORT || 8140) - 8140));
const server = `http://127.0.0.1:${port}/qcbin`;
const PATH = "/configurazione/integrazioni/alm";
const TITLE = "ALM / Quality Center";

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
  if (await listening()) throw new Error(`la porta ${port} è occupata: usa E2E_ALM_PORT`);
  const bin = resolve(root, process.env.E2E_BIN || "bin/regia");
  fake = spawn(bin, ["dev", "fake-alm", "--addr", `127.0.0.1:${port}`], { cwd: root, stdio: "ignore" });
  const until = Date.now() + 15_000;
  while (!(await listening())) {
    if (fake.exitCode !== null || Date.now() > until) throw new Error("il finto ALM non è partito");
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

test("credenziali, prova, unità confermata, sincronizzazione e test set del change", async ({ page }) => {
  await login(page, "admin");
  // Un'applicazione con il nome del progetto «Ordini_online»: nasce la proposta.
  await page.request.post("/api/v1/architecture/applications", { data: { name: "Ordini online" } });

  await page.goto(PATH);
  await expect(page.getByRole("heading", { name: TITLE, exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Nuova integrazione" }).click();
  const name = uniq("ALM finto");
  await page.getByLabel("Nome", { exact: true }).fill(name);
  await page.getByLabel("Indirizzo di ALM").fill(server);
  await expect(page.getByLabel("API key (da ALM 12.60)")).toBeChecked();
  await page.getByLabel("Client ID").fill("regia-finto");
  await page.getByLabel("Segreto dell'API key").fill("finto-segreto-sviluppo");
  await page.getByLabel("Dominio del progetto 1").fill("QUALITA");
  await page.getByLabel("Nome del progetto 1").fill("Ordini_online");
  await page.getByRole("button", { name: "Aggiungi un progetto" }).click();
  // Il dominio della nuova riga riprende quello della precedente.
  await expect(page.getByLabel("Dominio del progetto 2")).toHaveValue("QUALITA");
  await page.getByLabel("Nome del progetto 2").fill("Pagamenti");
  // Numero di change: campo user-01 del test set, predefinito.
  await expect(page.getByLabel("Nome del campo")).toHaveValue("user-01");
  await expect(page.getByLabel("Conserva il nome di chi ha eseguito i test")).not.toBeChecked();
  await page.getByRole("button", { name: "Crea l'integrazione" }).click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
  integration = new URL(page.url()).searchParams.get("sel") ?? "";
  expect(integration).not.toBe("");
  // Il segreto non torna mai.
  await expect(page.getByText("finto-segreto-sviluppo")).toHaveCount(0);

  // La prova legge le collezioni progetto per progetto.
  await page.getByRole("button", { name: "Prova la connessione" }).click();
  const ordini = page.getByRole("table", { name: /Collezioni lette di QUALITA\/Ordini_online/ });
  await expect(ordini).toBeVisible();
  await expect(ordini.getByText("test-sets")).toBeVisible();
  await expect(page.getByRole("table", { name: /Collezioni lette di QUALITA\/Pagamenti/ })).toBeVisible();
  await expect(page.getByText("non leggibile")).toHaveCount(0);

  // Unità: i progetti, con la proposta di associazione; la si conferma.
  await page.getByRole("link", { name: "Unità", exact: true }).first().click();
  await page.getByRole("button", { name: "Leggi le unità da ALM" }).click();
  const unit = page.getByRole("listitem").filter({ hasText: "QUALITA/Ordini_online" });
  await expect(unit.getByText(/Proposta: Ordini online/)).toBeVisible();
  await unit.getByRole("button", { name: "Conferma", exact: true }).click();
  await expect(unit.getByText(/Associata a Ordini online/)).toBeVisible();

  // Prima sincronizzazione.
  await page.getByRole("tab", { name: "Sincronizzazioni" }).click();
  await page.getByRole("button", { name: "Sincronizza adesso" }).click();
  await waitSynced(page);

  // Test set letti del change CHG0045120.
  await page.getByRole("tab", { name: "Test set letti" }).click();
  await page.getByLabel("Numero di change").fill("CHG0045120");
  const set = page.getByRole("listitem").filter({ has: page.getByText("Sistema - ordini", { exact: true }) });
  await expect(set).toBeVisible();
  await expect(set.getByText("CHG0045120")).toBeVisible();
  await expect(set.getByText("falliti 1")).toBeVisible();
  // Il test set di gennaio ha un altro change.
  await expect(page.getByText("Sistema - ordini gennaio")).toHaveCount(0);
});

test("a 375 px non scorre in orizzontale", async ({ page }) => {
  test.skip(!integration, "serve l'integrazione del test precedente");
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "admin");
  for (const search of [`tab=integrazioni&sel=${integration}`, "tab=unita", "tab=testset", "tab=sincronizzazioni"]) {
    await page.goto(`${PATH}?${search}&integrazione=${integration}`);
    await expect(page.getByRole("heading", { name: TITLE, exact: true })).toBeVisible();
    await page.waitForLoadState("networkidle");
    await expectNoHorizontalScroll(page);
  }
  // La prova da telefono: un elenco al posto della tabella.
  await page.goto(`${PATH}?tab=integrazioni&sel=${integration}`);
  await page.getByRole("button", { name: "Prova la connessione" }).click();
  await expect(page.getByRole("list", { name: /Collezioni lette di QUALITA\/Ordini_online/ })).toBeVisible();
  await expect(page.getByRole("table")).toBeHidden();
  await expectNoHorizontalScroll(page);
});
