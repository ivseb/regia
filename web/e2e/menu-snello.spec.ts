// Menu snello (web/src/nav.ts): una ventina di voci per l'amministratore, Configurazione ed Elenchi
// come indici, Portafoglio e Finanza a schede, azioni nelle testate delle pagine e in ⌘K, sezioni
// richiudibili ricordate nel browser. Gli indirizzi, «g» + lettera e i titoli delle pagine restano.
import { expect, test } from "@playwright/test";
import { login } from "./fixtures";

const NAV = "Navigazione principale";

test("l'amministratore ha un menu corto: indici, schede e azioni fuori dal menu", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/richieste");
  const nav = page.getByRole("navigation", { name: NAV });
  await expect(nav.getByRole("link", { name: "Cruscotto", exact: true })).toBeVisible();
  // Prima erano circa 59 voci; ora le pagine sorelle e le impostazioni sono dietro una voce sola.
  expect(await nav.locator(".nav-voce").count()).toBeLessThanOrEqual(26);
  for (const name of ["Portafoglio", "Finanza e budget", "Elenchi", "Configurazione"]) {
    await expect(nav.getByRole("link", { name, exact: true }), name).toBeVisible();
  }
  for (const name of ["Graduatoria", "Cabina di portafoglio", "Persone e accessi", "Webhook", "Nuova richiesta", "Nuovo progetto"]) {
    await expect(nav.getByRole("link", { name, exact: true }), name).toHaveCount(0);
  }
  // Le azioni sono nella testata delle pagine.
  // Le testate delle pagine (PageHeader), non il contenuto: lo stato vuoto può avere un pulsante uguale.
  const main = page.locator("main header");
  await expect(main.getByRole("link", { name: "Nuova richiesta" })).toBeVisible();
  await expect(main.getByRole("link", { name: "Portale richieste", exact: true })).toHaveAttribute("href", "/portale");
  await page.goto("/progetti");
  await expect(main.getByRole("link", { name: "Nuovo progetto" })).toBeVisible();

  // Portafoglio: tre schede; la voce resta attiva su ognuna e il titolo è quello della scheda.
  await nav.getByRole("link", { name: "Portafoglio", exact: true }).click();
  const tabs = page.getByRole("navigation", { name: "Portafoglio" });
  await expect(tabs.getByRole("link")).toHaveCount(3);
  await tabs.getByRole("link", { name: "Cabina di portafoglio" }).click();
  await expect(page).toHaveURL(/\/cabina$/);
  await expect(page.getByRole("heading", { name: "Cabina di portafoglio", level: 1 })).toBeVisible();
  await expect(nav.getByRole("link", { name: "Portafoglio", exact: true })).toHaveAttribute("aria-current", "page");

  // Finanza e budget: due schede.
  await nav.getByRole("link", { name: "Finanza e budget", exact: true }).click();
  await expect(page).toHaveURL(/\/finanza/);
  await page.getByRole("navigation", { name: "Finanza e budget" }).getByRole("link", { name: "Budget", exact: true }).click();
  await expect(page).toHaveURL(/\/budget/);
  await expect(nav.getByRole("link", { name: "Finanza e budget", exact: true })).toHaveAttribute("aria-current", "page");

  // Configurazione: l'indice porta alle pagine uscite dal menu; la voce resta attiva dentro.
  await nav.getByRole("link", { name: "Configurazione", exact: true }).click();
  await expect(page).toHaveURL(/\/configurazione$/);
  await page.getByRole("link", { name: "Apri Impostazioni dei progetti" }).click();
  await expect(page).toHaveURL(/\/configurazione\/progetti$/);
  await expect(nav.getByRole("link", { name: "Configurazione", exact: true })).toHaveAttribute("aria-current", "page");
  // Il titolo della scheda del browser viene dalla pagina, anche se non è più una voce del menu.
  await expect(page).toHaveTitle("Progetti · Regia");

  // Elenchi: l'indice con i sette elenchi.
  await nav.getByRole("link", { name: "Elenchi", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Elenchi", level: 1 })).toBeVisible();
  await expect(page.getByTestId("elenchi").locator(":scope > li")).toHaveCount(7);
  await page.locator('a[href="/elenco/rischi"]').first().click();
  await expect(page).toHaveURL(/\/elenco\/rischi/);
  await expect(nav.getByRole("link", { name: "Elenchi", exact: true })).toHaveAttribute("aria-current", "page");
});

test("«g» + lettera e ⌘K raggiungono anche le pagine fuori dal menu", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/richieste");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  // «g g» porta a Governance, che ora è una scheda del Portafoglio.
  await page.locator("body").press("g");
  await page.locator("body").press("g");
  await expect(page).toHaveURL(/\/governance$/);

  await page.keyboard.press("ControlOrMeta+k");
  const dialog = page.getByRole("dialog", { name: "Palette dei comandi" });
  const input = dialog.getByRole("combobox");
  await input.fill("nuovo progetto");
  await expect(dialog.getByRole("option", { name: /Nuovo progetto/ })).toBeVisible();
  await input.fill("persone e accessi");
  await expect(dialog.getByRole("option", { name: /Persone e accessi/ })).toBeVisible();
  await input.fill("elenchi");
  await dialog.getByRole("option", { name: /Elenchi/ }).first().click();
  await expect(page).toHaveURL(/\/elenco$/);
});

test("le sezioni si chiudono, restano chiuse dopo il ricaricamento e quella attiva è sempre aperta", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/richieste");
  const nav = page.getByRole("navigation", { name: NAV });
  // La sezione della pagina attiva (Demand) non si chiude.
  await expect(nav.getByRole("link", { name: "Cruscotto", exact: true })).toBeVisible();
  await expect(nav.getByRole("button", { name: "Demand", exact: true })).toHaveCount(0);

  const delivery = nav.getByRole("button", { name: "Delivery", exact: true });
  await expect(delivery).toHaveAttribute("aria-expanded", "true");
  await delivery.click();
  await expect(delivery).toHaveAttribute("aria-expanded", "false");
  await expect(nav.getByRole("link", { name: "Board dei team" })).toBeHidden();

  await page.reload();
  await expect(nav.getByRole("button", { name: "Delivery", exact: true })).toHaveAttribute("aria-expanded", "false");
  await expect(nav.getByRole("link", { name: "Board dei team" })).toBeHidden();

  // Sulla pagina di una sezione chiusa, la sezione si apre da sola.
  await page.goto("/delivery/board");
  await expect(nav.getByRole("link", { name: "Board dei team" })).toBeVisible();
  await expect(nav.getByRole("link", { name: "Board dei team" })).toHaveAttribute("aria-current", "page");
});

test.describe("telefono (375 px)", () => {
  test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

  test("le testate richiudibili sono bersagli da 44 px", async ({ page }) => {
    await login(page, "admin");
    await page.goto("/richieste");
    await page.locator("header").first().getByRole("button", { name: "Menu", exact: true }).click();
    const nav = page.getByRole("navigation", { name: NAV });
    for (const name of ["Delivery", "Sezione DevSecOps"]) {
      const box = await nav.getByRole("button", { name, exact: true }).boundingBox();
      expect(box!.height, `altezza di «${name}»`).toBeGreaterThanOrEqual(44);
    }
    const box = await nav.getByRole("link", { name: "Configurazione", exact: true }).boundingBox();
    expect(box!.height).toBeGreaterThanOrEqual(44);
  });
});
