// Temi: modo chiaro/scuro/sistema dal menu (nel browser) e tema dell'account dal profilo
// (sul server, vale su ogni dispositivo).
import { test, expect, type Page } from "@playwright/test";
import { login } from "./fixtures";

const html = (page: Page) => page.locator("html");
/** Luminanza approssimata dello sfondo della pagina (0 nero, 1 bianco). */
const bgLuma = (page: Page) =>
  page.evaluate(() => {
    const [r, g, b] = getComputedStyle(document.body).backgroundColor.match(/[\d.]+/g)!.map(Number);
    return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
  });

test("modo chiaro, scuro e come il sistema", async ({ page }) => {
  await login(page, "pmo");
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("/richieste");
  const modes = page.getByRole("radiogroup", { name: "Tema" });
  await modes.getByRole("radio", { name: "Scuro" }).click();
  await expect(html(page)).toHaveAttribute("data-theme", "dark");
  expect(await bgLuma(page)).toBeLessThan(0.3);
  await page.reload();
  await expect(html(page)).toHaveAttribute("data-theme", "dark");
  await expect(modes.getByRole("radio", { name: "Scuro" })).toHaveAttribute("aria-checked", "true");

  await modes.getByRole("radio", { name: "Chiaro" }).click();
  await expect(html(page)).toHaveAttribute("data-theme", "light");
  expect(await bgLuma(page)).toBeGreaterThan(0.7);

  // "Sistema" segue il sistema operativo, anche quando cambia.
  await modes.getByRole("radio", { name: "Sistema" }).click();
  await expect(html(page)).toHaveAttribute("data-theme", "light");
  await page.emulateMedia({ colorScheme: "dark" });
  await expect(html(page)).toHaveAttribute("data-theme", "dark");
});

test("tema dell'account dal profilo", async ({ page, browser }) => {
  await login(page, "pmo");
  await page.goto("/profilo");
  const group = page.getByRole("group", { name: "Tema" });
  const save = page.getByRole("button", { name: "Salva l'aspetto" });
  await group.getByRole("radio", { name: /^Petrolio/ }).check();
  await save.click();
  await expect(page.getByRole("status").filter({ hasText: "Salvato" })).toBeVisible();
  await expect(html(page)).toHaveAttribute("data-skin", "petrolio");
  // Salvato sull'account: vale anche in un altro browser (contesto nuovo, senza archivio locale).
  const other = await browser.newContext();
  const page2 = await other.newPage();
  await login(page2, "pmo");
  await page2.goto("/richieste");
  await expect(html(page2)).toHaveAttribute("data-skin", "petrolio");
  await other.close();
  await group.getByRole("radio", { name: /^Contrasto/ }).check();
  await save.click();
  await expect(html(page)).toHaveAttribute("data-skin", "contrasto");
  await group.getByRole("radio", { name: /^Come l'organizzazione/ }).check();
  await save.click();
  await expect(html(page)).toHaveAttribute("data-skin", "atlante");
});
