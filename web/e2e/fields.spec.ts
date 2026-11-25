// Campi personalizzati: l'amministratore crea un campo e lo associa alle richieste, il demand
// manager lo compila nel dettaglio e lo usa come filtro nella query.
import { test, expect } from "@playwright/test";
import { apiAs, login, newRequest, uniq, scegli } from "./fixtures";

test("campi personalizzati: crea, compila e filtra con la query", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const title = uniq("Rinnovo dei badge di accesso");
  const other = uniq("Rinnovo dei tornelli");
  const { key } = await newRequest(req, title);
  const { key: otherKey } = await newRequest(req, other);
  await req.dispose();

  const name = uniq("Utenti coinvolti");
  const fkey = name.toLowerCase().replace(/[^a-z0-9]+/g, "_");
  await login(page, "admin");
  await page.goto("/configurazione/campi");
  await page.getByRole("button", { name: "Nuovo campo" }).click();
  const editor = page.getByRole("complementary", { name: "Modifica del campo" });
  await editor.getByLabel("Nome", { exact: true }).fill(name);
  await expect(editor.getByLabel(/^Chiave/)).toHaveValue(fkey);
  await scegli(editor.getByLabel(/^Tipo di dato/), "Numero intero");
  await editor.getByRole("button", { name: "Crea il campo" }).click();

  // Associazione alle business request.
  const assoc = editor.locator("div.border", { hasText: "Richiesta" }).filter({ hasText: "business_request" }).first();
  await assoc.getByRole("button", { name: "Associa" }).click();
  await assoc.getByRole("button", { name: "Associa", exact: true }).last().click();
  await expect(assoc.getByText("Facoltativo")).toBeVisible();

  // Il campo compare nel dettaglio della richiesta e si compila sul posto.
  await page.goto(`/e/${key}`);
  await page.getByRole("button", { name: `Modifica ${name}` }).click();
  const form = page.locator("form").filter({ has: page.getByRole("button", { name: "Salva", exact: true }) }).first();
  await form.locator("input").first().fill("250");
  await form.getByRole("button", { name: "Salva", exact: true }).click();
  await expect(page.getByRole("button", { name: `Modifica ${name}` })).toContainText("250");

  // Filtro con la query: la richiesta con 250 utenti c'è, l'altra (vuota) no.
  await page.goto(`/richieste?q=${encodeURIComponent(`tipo: business_request ${fkey}: >100`)}`);
  await expect(page.getByRole("link", { name: key, exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: otherKey, exact: true })).toHaveCount(0);
  await page.goto(`/richieste?q=${encodeURIComponent(`tipo: business_request ${fkey}: <100`)}`);
  await expect(page.getByRole("heading", { name: "Richieste", level: 1 })).toBeVisible();
  await expect(page.getByRole("link", { name: key, exact: true })).toHaveCount(0);
  // Dalla barra della query, scrivendo.
  const bar = page.getByRole("combobox", { name: "QUERY" });
  await bar.fill(`${fkey}: 250`);
  await bar.press("Escape");
  await bar.press("Enter");
  await expect(page.getByRole("link", { name: key, exact: true })).toBeVisible();
});
