// Stage-gate: un passaggio bloccato mostra il motivo; risolto il motivo, la richiesta avanza.
import { test, expect } from "@playwright/test";
import { advance, apiAs, login, newRequest, uniq } from "./fixtures";

test("stage-gate: gate della stima bloccato con il motivo, poi avanzamento", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const title = uniq("Rinnovo del sistema di prenotazione sale");
  const { id, key } = await newRequest(req, title);
  await req.dispose();
  const dm = await apiAs(playwright, baseURL!, "dm");
  await advance(dm.req, id, "Triage");
  await dm.req.dispose();

  await login(page, "dm");
  await page.goto(`/e/${key}`);
  await expect(page.getByRole("heading", { name: title, level: 1 })).toBeVisible();
  await expect(page.getByText("Fase attuale: Triage")).toBeVisible();
  await page.getByRole("button", { name: "Avvia la valutazione" }).click();
  await expect(page.getByText("Fase attuale: Valutazione")).toBeVisible();

  // Senza stima il passaggio è bloccato: pulsante disattivato, motivo visibile e collegato al pulsante.
  const next = page.getByRole("button", { name: "Passa alla prioritizzazione" });
  await expect(next).toBeDisabled();
  await expect(next).toHaveAccessibleDescription(/serve una stima di costo/);
  await expect(page.getByText(/Passa alla prioritizzazione: .*serve una stima di costo/)).toBeVisible();

  // Il demand manager inserisce la stima: il gate si sblocca e la richiesta avanza.
  await page.getByRole("tab", { name: "Dettagli" }).click();
  await page.getByRole("button", { name: "Aggiorna la stima" }).click();
  await page.getByLabel("Minimo k€").fill("40");
  await page.getByLabel("Massimo k€").fill("60");
  await page.getByRole("button", { name: "Salva", exact: true }).click();
  await expect(page.getByText(/40\.000\s*€.*60\.000\s*€|40.*60.*k€/).first()).toBeVisible();
  await expect(next).toBeEnabled();
  await next.click();
  await expect(page.getByText("Fase attuale: Prioritizzazione")).toBeVisible();
  // La storia registra il passaggio.
  await page.getByRole("tab", { name: "Attività" }).click();
  await page.getByRole("radio", { name: "Tutto" }).click(); // l'attività si apre sui commenti
  await expect(page.getByRole("tabpanel", { name: "Attività" }).getByText(/Prioritizzazione/).first()).toBeVisible();
});
