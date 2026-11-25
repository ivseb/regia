// Il richiedente crea una richiesta dal portale e la vede tra le sue.
import { test, expect } from "@playwright/test";
import { call, login, uniq } from "./fixtures";

test("richiedente: nuova richiesta dal portale, invio e conferma", async ({ page }) => {
  await login(page, "richiedente");
  // Una sola bozza per tipo: «Inizia» riprende quella già aperta, quindi si parte senza bozze.
  const portal = await call(page.request, "GET", "/api/v1/portal");
  for (const d of portal.drafts) await call(page.request, "DELETE", `/api/v1/portal/drafts/${d.id}`);
  await page.goto("/richieste");
  // Il richiedente vede solo il portale: le pagine interne lo riportano lì.
  await expect(page).toHaveURL(/\/portale$/);
  await expect(page.getByRole("heading", { name: /^Ciao / })).toBeVisible();

  await page.getByRole("region", { name: "Nuova richiesta" }).getByRole("link", { name: "Inizia" }).first().click();
  await expect(page.getByRole("heading", { name: "Nuova richiesta", level: 1 })).toBeVisible();
  const title = uniq("Archivio digitale dei verbali");
  const next = page.getByRole("button", { name: "Avanti" });
  await expect(next).toBeDisabled();
  await page.getByRole("textbox", { name: /In una riga, cosa ti serve/ }).fill(title);
  await page.getByRole("textbox", { name: /Raccontaci il bisogno/ }).fill("Oggi i verbali sono su carta e si perdono tra gli uffici.");
  await next.click();
  await expect(page.getByRole("heading", { name: "Tempi e contesto", level: 2 })).toBeVisible();
  await next.click();
  await expect(page.getByRole("heading", { name: "Altri dettagli", level: 2 })).toBeVisible();
  await next.click();
  await expect(page.getByRole("heading", { name: "Controlla e invia", level: 2 })).toBeVisible();
  await expect(page.getByText(title)).toBeVisible();
  await page.getByRole("button", { name: /^Invia/ }).click();

  // Conferma: chiave assegnata, fase "Inviata" e la richiesta è tra "Le mie richieste".
  await expect(page).toHaveURL(/\/portale\/richieste\/DEM-\d+/);
  const key = /DEM-\d+/.exec(page.url())![0];
  await expect(page.getByRole("heading", { name: title })).toBeVisible();
  await expect(page.getByText("Inviata").first()).toBeVisible();
  await page.getByRole("link", { name: /portale richieste/ }).first().click();
  await expect(page.getByRole("region", { name: "Le mie richieste" }).getByRole("link", { name: new RegExp(`${key} ${title}`) })).toBeVisible();
});
