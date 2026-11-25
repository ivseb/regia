// Graduatoria: il cursore del budget sposta la linea di taglio e aggiorna finanziate, impegnato
// e margine; la simulazione si annulla senza salvare.
import { test, expect } from "@playwright/test";
import { apiAs, call, login, uniq } from "./fixtures";

const keur = (s: string) => Number(s.replace(/[^\d,]/g, "").replace(",", "."));

// La graduatoria è del tenant intero: nel tenant «demo» gli altri test creano richieste con stime
// di ogni cifra mentre questo gira. Qui si usa il tenant «altro» (solo di pochi test, che non
// creano richieste) con tre richieste proprie, di costo noto: 100, 250 e 150 k€.
test("graduatoria: cursore del budget e linea di taglio", async ({ page, playwright, baseURL }) => {
  const { req, me } = await apiAs(playwright, baseURL!, "admin", "altro");
  for (const [name, cents] of [["Prima", 10_000_000], ["Seconda", 25_000_000], ["Terza", 15_000_000]] as const) {
    const c = await call<{ id: string }>(req, "POST", "/api/v1/demand/requests", {
      title: uniq(`${name} richiesta in graduatoria`), description: "Richiesta creata dai test end-to-end.", sponsor_id: me.active_tenant!.member_id, fields: { urgenza: "Media" },
    });
    await call(req, "PATCH", `/api/v1/demand/requests/${c.id}`, { estimate_low_cents: cents, estimate_high_cents: cents });
    await call(req, "POST", `/api/v1/elements/${c.id}/transitions`, { to: "Inviata" });
  }
  await req.dispose();
  await login(page, "admin", "altro");
  await page.goto("/graduatoria");
  await expect(page.getByRole("heading", { name: "Graduatoria del portafoglio" })).toBeVisible();
  const panel = page.getByRole("region", { name: "Budget disponibile" });
  const slider = panel.getByRole("slider", { name: "Budget disponibile (cursore)" });
  const funded = panel.getByRole("definition").nth(0);
  const committed = panel.getByRole("definition").nth(1);
  const margin = panel.getByRole("definition").nth(2);
  const saved = await slider.inputValue();

  // Cumulata delle prime due richieste in graduatoria (ultima colonna).
  const rows = page.getByRole("table").getByRole("row");
  const cum1 = keur(await rows.nth(1).getByRole("cell").last().innerText());
  const cum2 = keur(await rows.nth(2).getByRole("cell").last().innerText());
  expect(cum2).toBeGreaterThan(cum1);

  // Senza budget salvato il cursore è già a zero: si parte da un valore diverso per avere un cambio.
  await slider.fill("100000000");
  await expect(funded).toHaveText(/^3 di 3$/);
  // Budget a zero: nessuna finanziata.
  await slider.fill("0");
  await expect(funded).toHaveText(/^0 di \d+$/);
  await expect(panel.getByText("simulazione")).toBeVisible();

  // Budget tra la prima e la seconda cumulata: finanziata solo la prima, margine coerente.
  const mid = Math.round((cum1 + (cum2 - cum1) / 2) * 100_000);
  await slider.fill(String(mid));
  await expect(funded).toHaveText(/^1 di \d+$/);
  await expect(committed).toHaveText(`${cum1} k€`);
  expect(keur(await margin.innerText())).toBeCloseTo(mid / 100_000 - cum1, 0);
  await expect(page.getByText(/Linea di taglio dopo la posizione 1: 1 richieste finanziate/)).toBeAttached();

  // Con la casella del budget si ottiene lo stesso effetto (in k€).
  await panel.getByRole("textbox", { name: /Budget disponibile/ }).fill(String(cum2));
  await expect(funded).toHaveText(/^2 di \d+$/);
  await expect(slider).toHaveValue(String(cum2 * 100_000));

  // Annullando si torna al budget salvato: la simulazione non tocca il server.
  await panel.getByRole("button", { name: /^(Torna a|Annulla)/ }).click();
  await expect(slider).toHaveValue(saved);
  await expect(panel.getByText("simulazione")).toHaveCount(0);
});
