// Lingua dell'interfaccia (replica/lingua.md, passo 2: shell, palette, Knowledge, architettura, import):
// con il browser in inglese e nessuna scelta salvata (vale Accept-Language) menu, ⌘K e le pagine
// principali non mostrano parole italiane tipiche dell'interfaccia.
import { test, expect, type Page } from "@playwright/test";
import { login } from "./fixtures";

test.use({ locale: "en-US" });

// Parole dell'interfaccia italiana che in inglese non devono comparire (i dati demo non le usano come testo fisso).
const ITALIANO = /\b(Annulla|Salva|Elimina|Nessun[ao]?|Nuov[ao]|Cerca|Chiudi|Apri|Importa|Spazi|Impostazioni|Modifica|Architettura|Configurazione|Crea|Aggiungi|Esporta|Conferma|Torna)\b/;

async function senzaItaliano(page: Page, dove: string) {
  const testo = await page.locator("body").innerText();
  const trovata = ITALIANO.exec(testo);
  expect(trovata, `${dove}: parola italiana «${trovata?.[0]}»`).toBeNull();
}

test("shell, ⌘K, Knowledge, mappa dell'architettura e import in inglese", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/");
  await expect(page.locator("html")).toHaveAttribute("lang", "en");

  // Menu laterale.
  const nav = page.getByRole("navigation").first();
  await expect(nav).toBeVisible();
  const menu = await nav.innerText();
  expect(ITALIANO.exec(menu), `menu: «${ITALIANO.exec(menu)?.[0]}»`).toBeNull();

  // ⌘K.
  await page.keyboard.press("ControlOrMeta+k");
  const dialog = page.getByRole("dialog", { name: "Command palette" });
  await expect(dialog).toBeVisible();
  const palette = await dialog.innerText();
  expect(ITALIANO.exec(palette), `palette: «${ITALIANO.exec(palette)?.[0]}»`).toBeNull();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();

  // Knowledge.
  await page.goto("/documenti");
  await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
  await senzaItaliano(page, "Knowledge");

  // Mappa dell'architettura.
  await page.goto("/architettura/mappa");
  await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
  await senzaItaliano(page, "mappa dell'architettura");

  // Import.
  await page.goto("/configurazione/import");
  await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
  await senzaItaliano(page, "import");
});
