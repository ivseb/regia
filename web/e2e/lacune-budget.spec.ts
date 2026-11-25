// Budget di progetto collegato a un nodo dell'albero dei budget, dall'interfaccia: «Attinge da» in creazione e nel
// dettaglio, residuo e avviso prima dell'invio, scollegamento con conferma, elenco nel pannello del nodo, 375 px.
import { test, expect, type APIRequestContext } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, newRequest, scegli, uniq } from "./fixtures";

// Ogni processo pesca da una fascia di anni tutta sua (gli esercizi non si sovrappongono).
const usedYears = new Set<number>();
function freshYear() {
  const base = 5000 + test.info().parallelIndex * 300;
  let y: number;
  do y = base + Math.floor(Math.random() * 300);
  while (usedYears.has(y));
  usedYears.add(y);
  return y;
}

/** Esercizio con plafond, periodo aperto, una richiesta e il suo nodo da 3.000 €. */
async function setup(req: APIRequestContext) {
  const fyName = uniq("ES");
  const year = freshYear();
  const fy = await call<{ id: string }>(req, "POST", "/api/v1/finance/fiscal-years", { name: fyName, start_date: `${year}-01-01`, end_date: `${year}-12-31`, envelope_cents: 100_000_000 });
  const { items } = await call<{ items: { id: string; root_id: string | null; status: string }[] }>(req, "GET", `/api/v1/budget/periods?fiscal_year_id=${fy.id}`);
  const period = items[0];
  if (period.status === "planning") await call(req, "POST", `/api/v1/budget/periods/${period.id}/open`);
  const r = await newRequest(req, uniq("Portale"));
  const node = await call<{ id: string }>(req, "POST", "/api/v1/budget/allocations", { period_id: period.id, parent_id: period.root_id, element_id: r.id, amount_cents: 300_000 });
  return { fyName, fyId: fy.id, periodId: period.id, key: r.key, nodeId: node.id };
}

async function nuovoBudget(page: import("@playwright/test").Page, s: Awaited<ReturnType<typeof setup>>) {
  await page.goto("/finanza");
  await scegli(page.getByRole("combobox", { name: "Esercizio", exact: true }), new RegExp(s.fyName));
  await page.getByRole("button", { name: "Nuovo budget" }).click();
  await page.getByLabel(/Richiesta o progetto/).fill(s.key);
  return page.getByRole("region", { name: "Attinge da" });
}

// Il budget salvato sul server, collegato al nodo: «data-linked» compare già nel modulo di
// creazione, prima del salvataggio, quindi si aspetta la risposta dell'API.
async function savedOnNode(req: APIRequestContext, s: { fyId: string; key: string; nodeId: string }) {
  await expect
    .poll(async () => {
      const { items } = await call<{ items: { allocation_id: string | null }[] }>(req, "GET", `/api/v1/finance/budgets?fiscal_year=${s.fyId}&element=${s.key}`);
      return items[0]?.allocation_id ?? null;
    }, { timeout: 15_000 })
    .toBe(s.nodeId);
}

test("collega un budget a un nodo in creazione, vede il residuo e l'avviso, scollega e ricollega", async ({ page, playwright, baseURL }) => {
  const req = (await apiAs(playwright, baseURL!, "finanza")).req;
  const s = await setup(req);
  await login(page, "finanza");
  const src = await nuovoBudget(page, s);

  // Il nodo si propone col suo percorso e il disponibile.
  await scegli(src.getByRole("combobox"), new RegExp(s.fyName));
  const numeri = src.getByTestId("link-numbers");
  await expect(numeri.locator('[data-k="available"]')).toContainText(/3\.?000/);
  const importo = page.getByLabel(/^Importo/);
  await importo.fill("2000");
  await expect(numeri.locator('[data-k="uses"]')).toContainText(/2\.?000/);
  await expect(numeri.locator('[data-k="after"]')).toContainText(/1\.?000/);
  await expect(src.getByTestId("link-exceeds")).toHaveCount(0);

  // Oltre il disponibile l'avviso compare prima dell'invio.
  await importo.fill("4000");
  await expect(src.getByTestId("link-exceeds")).toContainText(/1\.?000/);
  await importo.fill("2000");
  await expect(src.getByTestId("link-exceeds")).toHaveCount(0);

  await page.getByRole("button", { name: "Crea la bozza" }).click();
  const det = page.getByRole("region", { name: "Attinge da" });
  await expect(det).toHaveAttribute("data-linked", "yes");
  await expect(det.getByTestId("link-numbers").locator('[data-k="allocated"]')).toContainText(/3\.?000/);

  // Via API il budget ha il nodo.
  await savedOnNode(req, s);

  // Scollega, con conferma: annullando non cambia nulla.
  await det.getByRole("button", { name: "Scollega" }).click();
  await det.getByRole("button", { name: "Annulla" }).click();
  await expect(det).toHaveAttribute("data-linked", "yes");
  await det.getByRole("button", { name: "Scollega" }).click();
  await det.getByRole("button", { name: "Sì, scollega" }).click();
  await expect(det).toHaveAttribute("data-linked", "no");
  const dopo = await call<{ items: { allocation_id: string | null }[] }>(req, "GET", `/api/v1/finance/budgets?fiscal_year=${s.fyId}&element=${s.key}`);
  expect(dopo.items[0].allocation_id).toBeNull();

  // Ricollega dal dettaglio.
  await scegli(det.getByRole("combobox"), new RegExp(s.fyName));
  await expect(det).toHaveAttribute("data-linked", "yes");

  // Dal lato del nodo: l'elenco dei budget che ne attingono, con link all'elemento e stato.
  await page.goto(`/budget?periodo=${s.periodId}`);
  const row = page.locator("[data-node]").filter({ hasText: s.key }).first();
  await row.getByRole("button", { name: /^Storia di / }).click();
  await page.getByRole("tab", { name: "Budget di progetto" }).click();
  const panel = page.getByTestId("node-budgets");
  await expect(panel.getByRole("link", { name: s.key })).toBeVisible();
  await expect(panel.locator('[data-status="draft"]')).toBeVisible();
  await expect(panel).toContainText(/2\.?000/);
});

test("un budget oltre il nodo mostra l'avviso e l'invio viene rifiutato", async ({ page, playwright, baseURL }) => {
  const req = (await apiAs(playwright, baseURL!, "finanza")).req;
  const s = await setup(req);
  await login(page, "finanza");
  const src = await nuovoBudget(page, s);
  await scegli(src.getByRole("combobox"), new RegExp(s.fyName));
  await page.getByLabel(/^Importo/).fill("4000");
  await page.getByRole("button", { name: "Crea la bozza" }).click();
  const det = page.getByRole("region", { name: "Attinge da" });
  await expect(det.getByTestId("link-exceeds")).toBeVisible();
  await page.getByRole("button", { name: /Invia in approvazione/ }).click();
  await expect(page.getByRole("alert").filter({ hasText: /supera lo stanziamento/ })).toBeVisible();
});

test("a 375 px il collegamento si legge senza scorrimento orizzontale", async ({ page, playwright, baseURL }) => {
  const req = (await apiAs(playwright, baseURL!, "finanza")).req;
  const s = await setup(req);
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "finanza");
  const src = await nuovoBudget(page, s);
  await scegli(src.getByRole("combobox"), new RegExp(s.fyName));
  await page.getByLabel(/^Importo/).fill("2000");
  await expect(src.getByTestId("link-numbers")).toBeVisible();
  await expectNoHorizontalScroll(page);
  await page.getByRole("button", { name: "Crea la bozza" }).click();
  await expect(page.getByRole("region", { name: "Attinge da" })).toHaveAttribute("data-linked", "yes");
  await savedOnNode(req, s);
  await expectNoHorizontalScroll(page);

  await page.goto(`/budget?periodo=${s.periodId}`);
  // A questa larghezza l'albero è a carte: si scende nel plafond per vedere il nodo.
  await page.getByRole("button", { name: /^Scendi/ }).first().click();
  const row = page.locator("[data-node]").filter({ hasText: s.key }).first();
  await row.getByRole("button", { name: /^Storia di / }).click();
  await page.getByRole("tab", { name: "Budget di progetto" }).click();
  await expect(page.getByTestId("node-budgets")).toBeVisible();
  await expectNoHorizontalScroll(page);
});
