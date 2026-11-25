// Lingua dell'interfaccia, area lavoro (replica/lingua.md, passo 2): le pagine principali del
// lavoro si aprono in inglese (browser in inglese, nessuna scelta salvata) e nei testi principali
// non compaiono parole italiane tipiche dell'interfaccia.
import { test, expect, type Page } from "@playwright/test";
import { apiAs, login, newRequest, uniq } from "./fixtures";

test.use({ locale: "en-US" });

// «Salva» e «Modifica» restano fuori: vengono da ViewsBar e fields.tsx, non convertiti qui.
// Parole italiane che non devono comparire nel contenuto della pagina (il menu è della shell).
const ITALIANO = /\b(Annulla|Aggiungi|Elimina|Nessun[aoi]?|Settimana|Filtri|Assegnat[aoi]|Scadenza|Torna|Avanti|Indietro|Conferma|Allegat[oi]|Commenti)\b/;

async function senzaItaliano(page: Page, dove: string) {
  const testo = await page.locator("main").first().innerText();
  const trovato = ITALIANO.exec(testo);
  expect(trovato, `${dove}: parola italiana «${trovato?.[0]}»`).toBeNull();
}

test("dettaglio di una richiesta in inglese", async ({ page, playwright, baseURL }) => {
  const r = await apiAs(playwright, baseURL!, "richiedente");
  const rq = await newRequest(r.req, uniq("Inventory of company phones"));
  await r.req.dispose();
  await login(page, "pmo");
  await page.goto(`/richieste/${rq.key}`);
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("heading", { name: /Inventory of company phones/ }).first()).toBeVisible();
  await page.waitForLoadState("networkidle");
  await senzaItaliano(page, "dettaglio richiesta");
});

test("board, backlog e timesheet in inglese", async ({ page }) => {
  await login(page, "pmo");
  for (const path of ["/delivery/board", "/delivery/backlog", "/tempo", "/tempo/report"]) {
    await page.goto(path);
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await page.waitForLoadState("networkidle");
    await senzaItaliano(page, path);
  }
});

test("cruscotto e richieste in inglese", async ({ page }) => {
  await login(page, "pmo");
  for (const path of ["/cruscotto", "/richieste"]) {
    await page.goto(path);
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await page.waitForLoadState("networkidle");
    await senzaItaliano(page, path);
  }
});

test("portale del richiedente in inglese", async ({ page }) => {
  await login(page, "richiedente");
  for (const path of ["/portale", "/portale/nuova"]) {
    await page.goto(path);
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await page.waitForLoadState("networkidle");
    await senzaItaliano(page, path);
  }
});
