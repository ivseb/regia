// Analisi tecnica: si cita un asset logico, si apre l'impatto su un'applicazione proposta,
// l'impatto ha la sua pagina e, finché non è stimato, blocca la prioritizzazione.
import { test, expect } from "@playwright/test";
import { advance, apiAs, call, login, newRequest, uniq, scegli } from "./fixtures";

test("analisi tecnica: cita un asset e apri un impatto", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const title = uniq("Pagamento con wallet nell'app");
  const { id, key } = await newRequest(req, title);
  await req.dispose();
  const dm = await apiAs(playwright, baseURL!, "dm");
  await advance(dm.req, id, "Triage", "Valutazione");
  await call(dm.req, "PATCH", `/api/v1/demand/requests/${id}`, { estimate_low_cents: 1_000_000, estimate_high_cents: 2_000_000 });
  await dm.req.dispose();

  await login(page, "dm");
  await page.goto(`/e/${key}?scheda=analisi`);
  const panel = page.getByRole("tabpanel", { name: "Analisi" });
  await expect(panel.getByText("nessun asset logico citato")).toBeVisible();
  await panel.getByRole("button", { name: "cita un asset" }).click();
  await scegli(panel.getByRole("combobox", { name: "Asset logico da citare" }), "App mobile");
  await expect(panel.getByRole("button", { name: "Togli App mobile" })).toBeVisible();

  // Le applicazioni dell'asset sono proposte: si apre l'impatto sul modulo pagamenti.
  await expect(panel.getByText("Applicazioni degli asset citati, ancora senza impatto:")).toBeVisible();
  const proposal = panel.locator("div", { hasText: /^Modulo pagamenti/ }).last();
  await proposal.getByRole("button", { name: "apri impatto" }).click();
  const row = panel.getByRole("row", { name: /Modulo pagamenti/ });
  await expect(row).toBeVisible();
  await expect(row.getByRole("cell", { name: "Payments" })).toBeVisible();
  await expect(panel.getByText(/1 da stimare/)).toBeVisible();

  // L'impatto da stimare blocca la prioritizzazione con il motivo.
  const next = page.getByRole("button", { name: "Passa alla prioritizzazione" });
  await expect(next).toBeDisabled();
  await expect(next).toHaveAccessibleDescription(/impatti sulle applicazioni ancora da stimare/);

  // Si apre l'impatto: ha la sua pagina, legata alla richiesta.
  const impactKey = (await row.getByRole("link").first().innerText()).trim();
  await row.getByRole("link", { name: impactKey }).click();
  await expect(page).toHaveURL(new RegExp(`/e/${impactKey}`));
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Modulo pagamenti");
  await expect(page.getByRole("link", { name: new RegExp(key) }).first()).toBeVisible();
});
