// Il demand manager chiede l'approvazione (solo con i dati che servono dopo), lo sponsor la
// trova nell'inbox e la approva: la richiesta passa alla pianificazione.
import { test, expect } from "@playwright/test";
import { advance, apiAs, call, login, newRequest, uniq } from "./fixtures";

test("approvazione dello sponsor dall'inbox", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const title = uniq("Badge elettronici per i visitatori");
  const { id, key } = await newRequest(req, title);
  await req.dispose();
  // Sotto i 50 k€ basta lo sponsor (politica di default).
  const dm = await apiAs(playwright, baseURL!, "dm");
  await advance(dm.req, id, "Triage", "Valutazione");
  await call(dm.req, "PATCH", `/api/v1/demand/requests/${id}`, { estimate_low_cents: 2_000_000, estimate_high_cents: 3_000_000 });
  await advance(dm.req, id, "Prioritizzazione");

  // Regressione: senza la data di go-live (obbligatoria da Pianificazione) l'approvazione
  // non si chiede. Prima si apriva e lo sponsor riceveva un errore che non poteva risolvere.
  await login(page, "dm");
  await page.goto(`/e/${key}`);
  const ask = page.getByRole("button", { name: "Chiedi l'approvazione" });
  await expect(ask).toBeDisabled();
  await expect(ask).toHaveAccessibleDescription(/l'approvazione porta a «Pianificazione»: prima compila Data go-live richiesta/);
  const el = await call(dm.req, "GET", `/api/v1/elements/${id}`);
  await call(dm.req, "PATCH", `/api/v1/elements/${id}`, { version: el.element.version, fields: { data_golive: "2027-05-31" } });
  await dm.req.dispose();
  await page.reload();
  await expect(ask).toBeEnabled();
  await ask.click();
  await expect(page.getByText("Fase attuale: Approvazione")).toBeVisible();

  await page.context().clearCookies();
  await login(page, "sponsor");
  await page.goto("/approvazioni");
  await expect(page.getByRole("heading", { name: "Le mie approvazioni" })).toBeVisible();
  const row = page.getByRole("row", { name: new RegExp(`${key} ${title}`) });
  await expect(row).toBeVisible();
  await expect(row.getByRole("cell", { name: "30 k€" })).toBeVisible();
  await row.getByRole("link", { name: title }).click();

  await expect(page.getByRole("heading", { name: title, level: 1 })).toBeVisible();
  await page.getByRole("tab", { name: /Approvazioni/ }).click();
  await page.getByRole("button", { name: "Approva", exact: true }).click();
  await expect(page.getByText("Fase attuale: Pianificazione")).toBeVisible();

  await page.goto("/approvazioni");
  await expect(page.getByRole("heading", { name: "Le mie approvazioni" })).toBeVisible();
  await expect(page.getByRole("row", { name: new RegExp(`${key} `) })).toHaveCount(0);
});
