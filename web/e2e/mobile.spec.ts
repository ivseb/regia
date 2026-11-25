// A 375 px (telefono) le pagine chiave non scorrono in orizzontale: le tabelle larghe e il
// Gantt scorrono dentro il loro riquadro, non la pagina.
import { test, expect } from "@playwright/test";
import { expectNoHorizontalScroll, login } from "./fixtures";

test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

const pages: { who: string; path: string; ready: RegExp }[] = [
  { who: "dm", path: "/richieste", ready: /^Richieste$/ },
  { who: "dm", path: "/e/DEM-3", ready: /Portale clienti B2B/ },
  { who: "pmo", path: "/cruscotto", ready: /Cruscotto di portafoglio/ },
  { who: "pmo", path: "/graduatoria", ready: /Graduatoria del portafoglio/ },
  { who: "lead", path: "/delivery/board?spazio=WEB", ready: /Board · Web/ },
  { who: "dm", path: "/richieste/DEM-3/piano", ready: /Portale clienti B2B|Piano/ },
  { who: "richiedente", path: "/portale", ready: /^Ciao / },
];

for (const p of pages) {
  test(`375 px senza scorrimento orizzontale: ${p.path}`, async ({ page }) => {
    await login(page, p.who);
    await page.goto(p.path);
    await expect(page.getByRole("heading", { level: 1, name: p.ready })).toBeVisible();
    await page.waitForLoadState("networkidle");
    await expectNoHorizontalScroll(page);
    // Il menu a scomparsa si apre e si chiude senza allargare la pagina.
    const menu = page.getByRole("button", { name: "Menu" });
    if (await menu.count()) {
      await menu.click();
      await expect(page.getByRole("navigation", { name: "Navigazione principale" })).toBeVisible();
      await expectNoHorizontalScroll(page);
      await page.getByRole("button", { name: "Chiudi" }).first().click();
    }
  });
}
