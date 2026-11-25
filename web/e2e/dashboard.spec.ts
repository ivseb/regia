// Cruscotto: ogni numero porta alla lista delle richieste con la query giusta. Il perimetro
// è un'unità organizzativa creata dal test, così i conteggi non dipendono dagli altri test.
import { test, expect } from "@playwright/test";
import { advance, apiAs, call, login, newRequest, uniq, scegli } from "./fixtures";

test("cruscotto: un numero porta alla lista giusta", async ({ page, playwright, baseURL }) => {
  const admin = await apiAs(playwright, baseURL!, "admin");
  const unitName = uniq("Unità e2e");
  const unit = (await call(admin.req, "POST", "/api/v1/org-units", { name: unitName, kind: "business_unit" })).id as string;
  await admin.req.dispose();
  const r = await apiAs(playwright, baseURL!, "richiedente");
  const extra = { requesting_unit_id: unit };
  const a = await newRequest(r.req, uniq("Archivio dei disegni tecnici"), { extra });
  const b = await newRequest(r.req, uniq("Inventario dei telefoni aziendali"), { extra });
  const c = await newRequest(r.req, uniq("Turni del centralino"), { extra });
  await r.req.dispose();
  const dm = await apiAs(playwright, baseURL!, "dm");
  await advance(dm.req, c.id, "Triage");
  await dm.req.dispose();

  await login(page, "pmo");
  await page.goto("/cruscotto");
  await expect(page.getByRole("heading", { name: "Cruscotto di portafoglio" })).toBeVisible();
  await scegli(page.getByRole("form", { name: "Filtri" }).getByLabel("Unità"), unitName);
  await expect(page).toHaveURL(/[?&]unit=/);
  const summary = page.getByRole("region", { name: "In sintesi" });
  await expect(summary.getByRole("link", { name: "Richieste nel perimetro: 3. Apri la lista" })).toBeVisible();
  const phases = page.getByRole("region", { name: "Richieste per fase" });
  await expect(phases.getByRole("link", { name: "Triage: 1. Apri la lista" })).toBeVisible();

  // "Inviata: 2" apre la lista con la query della fase e dell'unità: proprio quelle due.
  await phases.getByRole("link", { name: "Inviata: 2. Apri la lista" }).click();
  await expect(page).toHaveURL(/\/richieste\?q=/);
  const q = await page.getByRole("combobox", { name: "QUERY" }).inputValue();
  expect(q).toMatch(/fase: "Inviata"/);
  const body = page.getByRole("table").getByRole("rowgroup").nth(1);
  await expect(body.getByRole("row")).toHaveCount(2);
  await expect(body.getByRole("link", { name: a.key, exact: true })).toBeVisible();
  await expect(body.getByRole("link", { name: b.key, exact: true })).toBeVisible();
  await expect(body.getByRole("link", { name: c.key, exact: true })).toHaveCount(0);
});
