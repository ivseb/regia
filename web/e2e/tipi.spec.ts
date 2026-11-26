// Configurazione → Tipi: rinomina con anteprima dal vivo (il nome nuovo compare in ⌘K), tipo nuovo
// da un modello in due passi, archiviazione e un caso a 375 px.
import { test, expect } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, uniq } from "./fixtures";

test("tipi: rinomina con anteprima, il nome nuovo compare in ⌘K", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const word = `zaffiro${Date.now().toString(36)}`;
  const cap = await call<{ id: string; key: string }>(req, "POST", "/api/v1/architecture/capabilities", { name: `Capability ${word}` });
  const before = (await call<{ items: { key: string; name: string; plural_name: string }[] }>(req, "GET", "/api/v1/types")).items.find((t) => t.key === "capability")!;
  const newName = uniq("Competenza");
  try {
    await login(page, "admin");
    await page.goto("/configurazione/tipi");
    await expect(page.getByRole("heading", { name: "Tipi di elemento", level: 1 })).toBeVisible();
    // Elenco raggruppato per modulo, con etichette di sistema.
    await expect(page.getByRole("region", { name: "Architettura" })).toContainText("di sistema");
    await page.getByRole("region", { name: "Architettura" }).getByRole("button", { name: /capability/ }).click();
    const scheda = page.getByRole("complementary", { name: "Scheda del tipo" });
    await expect(scheda.getByRole("button", { name: "Archivia il tipo" })).toBeDisabled(); // tipo di sistema con elementi

    await scheda.getByLabel("Nome", { exact: true }).fill(newName);
    await scheda.getByLabel("Nome al plurale").fill(`${newName} (plurale)`);
    await scheda.getByRole("button", { name: "Bersaglio" }).click();
    await scheda.getByRole("button", { name: "Verde" }).click();
    // Anteprima dal vivo: nome nell'elenco e nel chip.
    const preview = scheda.getByRole("region", { name: "Anteprima" });
    await expect(preview).toContainText(newName);
    await expect(preview).toContainText(`${newName} (plurale)`);
    await scheda.getByRole("button", { name: "Pubblica", exact: true }).click();
    await expect(scheda.getByText("Pubblicato.", { exact: true })).toBeVisible();
    // La riga dell'elenco mostra il nome nuovo; la chiave non cambia.
    await expect(page.getByRole("region", { name: "Architettura" }).getByRole("button", { name: new RegExp(newName) })).toContainText("capability");

    // ⌘K: il nome nuovo è quello del tipo, e il gruppo dei risultati è il plurale.
    await page.goto("/richieste");
    await expect(page.getByRole("heading", { name: "Richieste", level: 1 })).toBeVisible();
    await page.getByRole("button", { name: /Cerca o vai a/ }).click();
    const dialog = page.getByRole("dialog", { name: "Palette dei comandi" });
    await dialog.getByRole("combobox", { name: "Cerca elementi, pagine e azioni" }).fill(word);
    await expect(dialog.getByRole("option", { name: new RegExp(`${newName}.*${cap.key}`) })).toBeVisible();
    await expect(dialog.getByText(`${newName} (plurale)`, { exact: true })).toBeVisible();
  } finally {
    await call(req, "PATCH", "/api/v1/types/capability", { name: before.name, plural_name: before.plural_name, icon: "", color: "" });
    await req.dispose();
  }
});

test("tipi: nuovo tipo da un modello, in due passi, poi archiviazione", async ({ page, playwright, baseURL }) => {
  const name = uniq("Pratica interna");
  await login(page, "admin");
  await page.goto("/configurazione/tipi");
  await page.getByRole("button", { name: "Nuovo tipo" }).click();
  const scheda = page.getByRole("complementary", { name: "Scheda del tipo" });
  await expect(scheda.getByRole("heading", { name: "Da dove parti?" })).toBeVisible();
  await scheda.getByRole("radio", { name: /^Task/ }).check();
  await scheda.getByRole("button", { name: "Continua" }).click();
  await expect(scheda.getByRole("heading", { name: "Come si chiama?" })).toBeVisible();
  await scheda.getByLabel("Nome", { exact: true }).fill(name);
  const key = name.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "");
  await expect(scheda.getByRole("textbox", { name: /^Chiave/ })).toHaveValue(key);
  await scheda.getByRole("button", { name: "Crea il tipo" }).click();
  // Il tipo nuovo si apre nella sua scheda e compare nel gruppo del modulo.
  await expect(scheda.getByRole("heading", { name, level: 2 })).toBeVisible();
  await expect(page.getByRole("region", { name: "Work e delivery" }).getByRole("button", { name: new RegExp(name) })).toBeVisible();
  // Collegamenti ai campi e al processo copiato dal modello.
  await expect(scheda.getByRole("link", { name: /^Campi di questo tipo/ })).toBeVisible();
  await expect(scheda.getByRole("link", { name: new RegExp(`Processo di ${name}`) })).toBeVisible();

  // Archiviazione: etichetta «archiviato» e sparisce dall'elenco finché non si mostrano gli archiviati.
  await scheda.getByRole("button", { name: "Archivia il tipo" }).click();
  await expect(scheda.getByRole("button", { name: "Riattiva il tipo" })).toBeVisible();
  await page.getByRole("button", { name: "Chiudi" }).click();
  await expect(page.getByRole("button", { name: new RegExp(name) })).toHaveCount(0);
  await page.getByLabel("Mostra gli archiviati").check();
  await expect(page.getByRole("button", { name: new RegExp(name) })).toContainText("archiviato");
});

test.describe("375 px", () => {
  test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });
  test("tipi: elenco e scheda senza scorrimento orizzontale", async ({ page }) => {
    await login(page, "admin");
    await page.goto("/configurazione/tipi");
    await expect(page.getByRole("heading", { name: "Tipi di elemento", level: 1 })).toBeVisible();
    await page.waitForLoadState("networkidle");
    await expectNoHorizontalScroll(page);
    await page.getByRole("button", { name: /^Bug/ }).first().click();
    const scheda = page.getByRole("complementary", { name: "Scheda del tipo" });
    await expect(scheda.getByLabel("Nome", { exact: true })).toBeVisible();
    await expectNoHorizontalScroll(page);
  });
});
