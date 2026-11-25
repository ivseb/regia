// Palette dei comandi (⌘K / Ctrl+K): cerca un elemento per chiave e per parole del titolo,
// porta alle pagine, si chiude con Esc.
import { test, expect } from "@playwright/test";
import { apiAs, login, newRequest, uniq } from "./fixtures";

test("palette ⌘K: elementi, pagine e azioni", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const word = `zefiro${Date.now().toString(36)}`;
  const title = `Integrazione ${word} con il protocollo`;
  const { key } = await newRequest(req, title);
  await req.dispose();

  await login(page, "dm");
  await page.goto("/richieste");
  await expect(page.getByRole("heading", { name: "Richieste", level: 1 })).toBeVisible();
  const dialog = page.getByRole("dialog", { name: "Palette dei comandi" });
  const input = dialog.getByRole("combobox", { name: "Cerca elementi, pagine e azioni" });

  // Per chiave.
  await page.keyboard.press("ControlOrMeta+k");
  await expect(dialog).toBeVisible();
  await input.fill(key);
  await expect(dialog.getByRole("option", { name: new RegExp(`${key}.*${word}`) })).toBeVisible();
  await input.press("Enter");
  await expect(page).toHaveURL(new RegExp(`/(e|richieste)/${key}$`));
  await expect(page.getByRole("heading", { name: title, level: 1 })).toBeVisible();

  // Per parola del titolo (prefisso).
  await page.keyboard.press("ControlOrMeta+k");
  await input.fill(word.slice(0, 9));
  await expect(dialog.getByRole("option", { name: new RegExp(key) })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);

  // Pagine: "cruscotto" porta al cruscotto.
  await page.getByRole("button", { name: /Cerca o vai a/ }).click();
  await input.fill("cruscotto");
  const opt = dialog.getByRole("group", { name: "Pagine" }).getByRole("option", { name: /Cruscotto/ }).first();
  await expect(opt).toBeVisible();
  await opt.click();
  await expect(page).toHaveURL(/\/cruscotto/);

  // Regressione: le pagine aggiunte dopo la palette (board, valutazioni, requisiti…) mancavano.
  await page.keyboard.press("ControlOrMeta+k");
  await input.fill("board");
  await dialog.getByRole("group", { name: "Pagine" }).getByRole("option", { name: /Board dei team/ }).first().click();
  await expect(page).toHaveURL(/\/delivery\/board/);

  // Azioni: "nuova richiesta".
  await page.keyboard.press("ControlOrMeta+k");
  await input.fill("nuova richiesta");
  await dialog.getByRole("option", { name: /Nuova richiesta/ }).first().click();
  await expect(page).toHaveURL(/\/richieste\/nuova|\/portale\/nuova/);
});

test("palette ⌘K: azioni sull'elemento aperto", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const { key } = await newRequest(req, uniq("Prenotazione delle sale riunioni"));
  await req.dispose();
  await login(page, "dm");
  await page.goto(`/e/${key}`);
  await expect(page.getByText("Fase attuale: Inviata")).toBeVisible();
  await page.keyboard.press("ControlOrMeta+k");
  const dialog = page.getByRole("dialog", { name: "Palette dei comandi" });
  await dialog.getByRole("combobox").fill("carico");
  await dialog.getByRole("option", { name: /Prendi in carico/ }).first().click();
  await expect(page.getByText("Fase attuale: Triage")).toBeVisible();
});
