// Lingua dell'interfaccia (replica/lingua.md, passo 2), area configurazione: le pagine di
// configurazione e amministrazione si aprono in inglese (browser in inglese, nessuna scelta
// personale) e non lasciano parole italiane del prodotto. I nomi dei tipi, dei campi e dei
// processi sono dati dell'organizzazione e possono restare in italiano.
import { test, expect, type Page } from "@playwright/test";
import { call, login } from "./fixtures";

test.use({ locale: "en-US" });

// Parole dell'interfaccia (non dei dati) che in inglese non devono comparire.
const ITALIANO = /\b(Salva|Elimina|Archivia|Aggiungi|Cerca|Chiudi|Descrizione|Facoltativo|Facoltativa|Obbligatorio|Obbligatoria|Scegli|Duplica|Importa|Esporta|Nessuno|Nessun|Persone|Ruoli sugli oggetti|Sicurezza|Avanzato|Anteprima|Crea il|Modifica|Passaggi|Regole|Proprietà|Gruppi)\b/;

async function paginaInInglese(page: Page, path: string, titolo: string | RegExp) {
  await login(page, "admin");
  await page.goto(path);
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("heading", { name: titolo, level: 1 })).toBeVisible();
}

async function senzaItaliano(page: Page) {
  const testo = await page.getByRole("main").innerText();
  const m = ITALIANO.exec(testo);
  const trovata = m?.[0] ?? null;
  expect(trovata, `parola italiana «${trovata}» in: …${testo.slice(Math.max(0, (m?.index ?? 0) - 80), (m?.index ?? 0) + 80)}…`).toBeNull();
}

test("processi: elenco ed editor in inglese", async ({ page }) => {
  await paginaInInglese(page, "/processi", "Processes");
  await expect(page.getByRole("button", { name: "New process" })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "Priority" })).toBeVisible();
  await page.getByRole("button", { name: "New process" }).click();
  await expect(page.getByRole("region", { name: "New process" })).toBeVisible();
  await expect(page.getByText("From scratch")).toBeVisible();
  await expect(page.getByRole("button", { name: "Create and open the editor" })).toBeVisible();
  await senzaItaliano(page);

  // L'editor visuale di un processo esistente.
  const list = await call(page.request, "GET", "/api/v1/processes");
  const key = list.items[0].key as string;
  await page.goto(`/processi/${key}`);
  await expect(page.getByRole("button", { name: "+ Stage" })).toBeVisible();
  await expect(page.getByRole("complementary", { name: "Properties of the selected item" })).toBeVisible();
  await expect(page.getByText("Select a stage or a transition to edit it.")).toBeVisible();
  await expect(page.getByText("Issues", { exact: true }).first()).toBeVisible();
  await senzaItaliano(page);
});

test("campi personalizzati in inglese", async ({ page }) => {
  await paginaInInglese(page, "/configurazione/campi", "Custom fields");
  await expect(page.getByRole("button", { name: "New field" })).toBeVisible();
  await expect(page.getByPlaceholder("Search by name, key, or alias")).toBeVisible();
  await page.getByRole("button", { name: "New field" }).click();
  await expect(page.getByText("Set of values")).toHaveCount(0);
  await expect(page.getByText("Data type")).toBeVisible();
  await expect(page.getByText("Choose the type: each entry has an example.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Create the field" })).toBeVisible();
  await senzaItaliano(page);
});

test("tipi di elemento in inglese", async ({ page }) => {
  await paginaInInglese(page, "/configurazione/tipi", /Types|Item types/);
  await expect(page.getByRole("button", { name: "New type" })).toBeVisible();
  await expect(page.getByText("Every type has a name, an icon, and a color")).toBeVisible();
  await page.getByRole("button", { name: "New type" }).click();
  await expect(page.getByText("Where do you start?")).toBeVisible();
  await expect(page.getByRole("button", { name: "Continue" })).toBeVisible();
  await senzaItaliano(page);
});

test("persone e accessi: schede e ruoli in inglese", async ({ page }) => {
  await paginaInInglese(page, "/configurazione/persone", "People and access");
  await expect(page.getByRole("tab", { name: "Members" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Roles on items" })).toBeVisible();
  await page.getByRole("tab", { name: "Roles", exact: true }).click();
  await expect(page.getByRole("button", { name: "New role" })).toBeVisible();
  await expect(page.getByText("System roles can't be edited")).toBeVisible();
  await senzaItaliano(page);
  await page.getByRole("tab", { name: "Security" }).click();
  await expect(page.getByRole("heading", { name: "Two-factor authentication (MFA)" })).toBeVisible();
  await expect(page.getByText("Required for administrators")).toBeVisible();
});

test("moduli, registro di audit e webhook in inglese", async ({ page }) => {
  await paginaInInglese(page, "/configurazione/moduli", "Modules");
  await expect(page.getByText("Choose which parts of Regia your organization uses.")).toBeVisible();
  await senzaItaliano(page);

  await page.goto("/configurazione/audit");
  await expect(page.getByRole("heading", { name: "Audit log", level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "Verify the chain" })).toBeVisible();
  await senzaItaliano(page);

  await page.goto("/configurazione/webhook");
  await expect(page.getByRole("heading", { name: "Webhooks", level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "New webhook" })).toBeVisible();
  await senzaItaliano(page);
});
