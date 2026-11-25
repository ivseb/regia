// Budget gerarchico, interfaccia (replica/budget.md §6, passi 1-4): organizzazione, albero dei budget con
// modifica in linea e «Ripartisci» con anteprima, visibilità per sottoalbero, chiusura guidata,
// graduatoria per area, un caso a 375 px e una pagina in inglese.
import { test, expect, type APIRequestContext, type Playwright } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, memberId, newRequest, scegli, uniq } from "./fixtures";

type Setup = { fyName: string; periodId: string; rootId: string; area: { id: string; name: string }; rep: { id: string; name: string }; repNodeId?: string; areaNodeId: string };

// Gli esercizi non possono sovrapporsi e i processi dei test condividono il server: ogni processo (parallelIndex)
// pesca da una fascia di mille anni tutta sua, lontana dall'anno corrente, e non ripete mai un anno.
const usedYears = new Set<number>();
function freshYear(late = false) {
  const slot = test.info().parallelIndex;
  const base = late ? 9900 : 3000 + slot * 1000;
  const span = late ? 90 : 1000;
  let y: number;
  do y = base + Math.floor(Math.random() * span);
  while (usedYears.has(y));
  usedYears.add(y);
  return y;
}

/** Esercizio nuovo (con plafond di 1.000.000 €), un'area e un reparto, con Paolo Riva responsabile del reparto. */
async function setup(req: APIRequestContext, opts: { repAmount?: number; manager?: string; late?: boolean } = {}): Promise<Setup> {
  const areaName = uniq("Area");
  const repName = uniq("Reparto");
  const area = await call<{ id: string }>(req, "POST", "/api/v1/org/units", { name: areaName, kind: "business_unit" });
  const rep = await call<{ id: string }>(req, "POST", "/api/v1/org/units", { name: repName, kind: "department", parent_id: area.id });
  const lead = await memberId(req, opts.manager ?? "Paolo Riva");
  await call(req, "PUT", `/api/v1/org/units/${rep.id}/members/${lead}`, { role: "manager" });
  const fyName = uniq("ES");
  const YEAR = freshYear(opts.late);
  const fy = await call<{ id: string }>(req, "POST", "/api/v1/finance/fiscal-years", { name: fyName, start_date: `${YEAR}-01-01`, end_date: `${YEAR}-12-31`, envelope_cents: 100_000_000 });
  const { items } = await call<{ items: { id: string; root_id: string | null; status: string }[] }>(req, "GET", `/api/v1/budget/periods?fiscal_year_id=${fy.id}`);
  const period = items[0];
  if (period.status === "planning") await call(req, "POST", `/api/v1/budget/periods/${period.id}/open`);
  const areaNode = await call<{ id: string }>(req, "POST", "/api/v1/budget/allocations", { period_id: period.id, parent_id: period.root_id, org_unit_id: area.id, amount_cents: 40_000_000 });
  const out: Setup = { fyName, periodId: period.id, rootId: period.root_id!, area: { id: area.id, name: areaName }, rep: { id: rep.id, name: repName }, areaNodeId: areaNode.id };
  if (opts.repAmount) {
    const n = await call<{ id: string }>(req, "POST", "/api/v1/budget/allocations", { period_id: period.id, parent_id: areaNode.id, org_unit_id: rep.id, amount_cents: opts.repAmount });
    out.repNodeId = n.id;
  }
  return out;
}

async function admin(playwright: Playwright, baseURL: string) {
  return (await apiAs(playwright, baseURL, "admin")).req;
}

test("organizzazione: crea un'area e un reparto, sposta, e nomina una persona responsabile", async ({ page }) => {
  await login(page, "admin");
  const area = uniq("Area");
  const area2 = uniq("Area");
  const rep = uniq("Reparto");
  await page.goto("/configurazione/organizzazione");
  await expect(page.getByRole("heading", { name: "Organizzazione", level: 1 })).toBeVisible();

  // Due aree in cima.
  for (const name of [area, area2]) {
    await page.getByRole("button", { name: "Aggiungi un'unità in cima" }).click();
    await page.getByLabel("Nome", { exact: true }).fill(name);
    await page.getByLabel("Codice centro di costo").fill(`CC-${Math.floor(Math.random() * 1e7)}`);
    await page.getByRole("button", { name: "Crea l'unità" }).click();
    await expect(page.locator(`[data-unit="${name}"]`)).toBeVisible();
  }
  // Il reparto sotto la prima.
  await page.getByRole("button", { name: `Aggiungi un'unità sotto ${area}` }).click();
  await page.getByLabel("Nome", { exact: true }).fill(rep);
  await page.getByRole("button", { name: "Crea l'unità" }).click();
  await expect(page.locator(`[data-unit="${rep}"]`)).toBeVisible();

  // Una persona responsabile del reparto.
  await page.getByRole("button", { name: `Persone di ${rep}` }).click();
  const panel = page.getByRole("region", { name: `Persone di ${rep}` });
  await scegli(panel.getByRole("combobox", { name: "Persona", exact: true }), "Paolo Riva", { cerca: "Paolo" });
  await scegli(panel.getByRole("combobox", { name: "Ruolo", exact: true }), "Responsabile");
  await panel.getByRole("button", { name: "Aggiungi la persona" }).click();
  await expect(panel.getByText("Paolo Riva")).toBeVisible();
  await expect(page.locator(`[data-unit="${rep}"]`)).toContainText("1 persona");

  // La ricerca di una persona mostra le sue unità.
  await page.getByLabel("Nome o email").fill("Paolo Riva");
  await expect(page.getByRole("region", { name: "Cerca una persona" }).getByText(new RegExp(`${area}.*${rep}`))).toBeVisible();

  // «Sposta in…»: il reparto passa alla seconda area.
  await page.getByRole("button", { name: `Sposta ${rep} in un'altra unità` }).click();
  await scegli(page.getByLabel("Nuova unità madre"), new RegExp(area2), { cerca: area2 });
  await page.getByRole("button", { name: "Sposta", exact: true }).click();
  await expect(page.getByRole("region", { name: "Cerca una persona" }).getByText(new RegExp(`${area2}.*${rep}`))).toBeVisible();
});

test("organizzazione: l'import CSV mostra l'anteprima e si conferma", async ({ page }) => {
  await login(page, "admin");
  const a = uniq("Imp");
  const code = `IMP-${Math.floor(Math.random() * 1e7)}`;
  await page.goto("/configurazione/organizzazione");
  await page.getByLabel("Contenuto del file CSV").fill(`nome;codice;tipo\n${a};${code};area\n`);
  await page.getByRole("button", { name: "Anteprima" }).click();
  await expect(page.getByText("1 da creare")).toBeVisible();
  await page.getByRole("button", { name: "Conferma l'import" }).click();
  await expect(page.getByText("Import eseguito.")).toBeVisible();
  await expect(page.locator(`[data-unit="${a}"]`)).toBeVisible();
});

test("stanziamento in linea e ripartizione con anteprima", async ({ page, playwright, baseURL }) => {
  const setupReq = await admin(playwright, baseURL!);
  const s = await setup(setupReq);
  await login(page, "finanza");
  await page.goto(`/budget?periodo=${s.periodId}`);
  await expect(page.getByRole("heading", { name: "Budget per area", level: 1 })).toBeVisible();
  const areaRow = page.locator(`[data-node="${s.area.name}"]`);
  await expect(areaRow).toBeVisible();

  // Modifica in linea: il padre mostra subito la nuova riserva (plafond 1.000.000 − 500.000).
  await areaRow.getByRole("button", { name: new RegExp(`Modifica lo stanziato di ${s.area.name}`) }).click();
  const input = page.getByLabel(`Nuovo stanziato di ${s.area.name}`);
  await input.fill("500000");
  await expect(page.getByText(/Riserva di «.*»: 500\.000/)).toBeVisible();
  await page.getByRole("button", { name: "Salva", exact: true }).click();
  await expect(areaRow).toContainText("500.000");

  // «Ripartisci» in parti uguali: l'anteprima mostra il reparto come nuovo, poi si conferma.
  await areaRow.getByRole("button", { name: `Ripartisci ${s.area.name}` }).click();
  const dist = page.getByRole("form", { name: `Ripartisci ${s.area.name}` });
  await dist.getByRole("button", { name: "Anteprima" }).click();
  await expect(dist.getByText("L'anteprima è valida")).toBeVisible();
  await expect(dist.getByText(s.rep.name)).toBeVisible();
  await expect(dist.getByText("nuovo")).toBeVisible();
  await dist.getByRole("button", { name: "Conferma la ripartizione" }).click();
  await expect(dist.getByText(/Ripartito: 1 nodo aggiornato/)).toBeVisible();
  const repRow = page.locator(`[data-node="${s.rep.name}"]`);
  await expect(repRow).toContainText("500.000");

  // Un importo oltre lo stanziato del nodo: l'anteprima non è valida e la conferma resta chiusa.
  await dist.getByLabel("Importo da ripartire").fill("900000");
  await dist.getByRole("button", { name: "Anteprima" }).click();
  await expect(dist.getByText(/Non si può confermare/)).toBeVisible();
  await expect(dist.getByRole("button", { name: "Conferma la ripartizione" })).toBeDisabled();

  // Per percentuali: oltre il 100% il totale diventa rosso e l'anteprima non parte.
  await scegli(dist.getByLabel("Come ripartire"), "Per percentuali");
  await dist.getByLabel(`Percentuale di ${s.rep.name}`).fill("150");
  await expect(dist.getByText("Totale assegnato: 150%")).toBeVisible();
  await expect(dist.getByRole("button", { name: "Anteprima" })).toBeDisabled();
});

test("un responsabile di reparto vede solo il suo sottoalbero, anche nel cruscotto", async ({ page, playwright, baseURL }) => {
  const setupReq = await admin(playwright, baseURL!);
  const s = await setup(setupReq, { repAmount: 10_000_000 });
  // Il tree via API: solo il nodo del reparto.
  await login(page, "lead");
  const tree = await call<{ items: { label: string; kind: string }[] }>(page.request, "GET", `/api/v1/budget/tree?period_id=${s.periodId}`);
  expect(tree.items.map((n) => n.label)).toEqual([s.rep.name]);

  await page.goto(`/budget?periodo=${s.periodId}`);
  await expect(page.locator(`[data-node="${s.rep.name}"]`)).toBeVisible();
  await expect(page.locator(`[data-node="${s.area.name}"]`)).toHaveCount(0);
  await expect(page.getByText(/Plafond|plafond/)).toHaveCount(0);

  // «Il mio budget» nel cruscotto.
  await page.goto("/cruscotto");
  const mine = page.getByRole("region", { name: "Il mio budget" }).locator(`[data-mine="${s.rep.name}"]`);
  await expect(mine).toBeVisible();
  await expect(mine).toContainText("100.000");
});

test("chiusura guidata del periodo: anteprima, conferma e annullamento", async ({ page, playwright, baseURL }) => {
  const setupReq = await admin(playwright, baseURL!);
  const s = await setup(setupReq, { repAmount: 30_000_000, late: true });
  await login(page, "finanza");
  await page.goto(`/budget?periodo=${s.periodId}`);
  await page.getByRole("button", { name: "Chiudi il periodo" }).click();
  const wiz = page.getByRole("region", { name: /^Chiusura di / });
  await expect(wiz.getByText("Torna al padre").first()).toBeVisible();
  await expect(wiz.getByRole("row", { name: new RegExp(s.rep.name) })).toBeVisible();
  await wiz.getByRole("button", { name: "Avvia la chiusura" }).click();
  await wiz.getByRole("button", { name: "Conferma la chiusura" }).click();
  await expect(wiz.getByText(/Periodo chiuso/)).toBeVisible();
  await wiz.getByRole("button", { name: "Chiudi", exact: true }).click();
  await expect(page.getByRole("button", { name: "Annulla la chiusura" })).toBeVisible();
  await page.getByRole("button", { name: "Annulla la chiusura" }).click();
  await expect(page.getByRole("button", { name: "Annulla la chiusura" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Chiusura guidata|Chiudi il periodo/ })).toBeVisible();
});

test("graduatoria per area e periodo: «cosa entra» usa il residuo dello stanziamento dell'area", async ({ page, playwright, baseURL }) => {
  const setupReq = await admin(playwright, baseURL!);
  const s = await setup(setupReq);
  const { req: dm } = await apiAs(playwright, baseURL!, "dm");
  const inArea = await newRequest(dm, uniq("Richiesta d'area"));
  const outside = await newRequest(dm, uniq("Richiesta altrove"));
  await call(dm, "PATCH", `/api/v1/demand/requests/${inArea.id}`, { requesting_unit_id: s.rep.id, budget_period_id: s.periodId, estimate_low_cents: 50_000_000, estimate_high_cents: 60_000_000 });
  await call(dm, "PATCH", `/api/v1/demand/requests/${outside.id}`, { estimate_low_cents: 10_000_000, estimate_high_cents: 10_000_000 });

  await login(page, "finanza");
  await page.goto("/graduatoria");
  await expect(page.getByRole("link", { name: new RegExp(`^${outside.key}$|Richiesta altrove`) }).first()).toBeVisible();
  // Con l'area scelta compaiono solo le sue richieste e il budget è il residuo dello stanziamento.
  await scegli(page.getByRole("combobox", { name: "Area", exact: true }), new RegExp(s.area.name), { cerca: s.area.name });
  await scegli(page.getByRole("combobox", { name: "Periodo", exact: true }), new RegExp(s.fyName));
  const src = page.locator('[data-budget-source="allocation"]');
  await expect(src).toContainText(s.area.name);
  await expect(src).toContainText("400.000");
  await expect(page.getByText(inArea.key, { exact: true }).first()).toBeVisible();
  await expect(page.getByText(outside.key, { exact: true })).toHaveCount(0);

  // La scheda della richiesta ha unità e periodo, e propone lo stanziamento.
  await page.goto(`/e/${inArea.key}`);
  await page.getByRole("tab", { name: "Dettagli" }).click();
  await expect(page.getByText(new RegExp(`Attinge da «${s.area.name}»`))).toBeVisible();
});

test("a 375 px l'albero è a carte, con discesa nei figli, senza scorrimento orizzontale", async ({ page, playwright, baseURL }) => {
  const setupReq = await admin(playwright, baseURL!);
  const s = await setup(setupReq, { repAmount: 10_000_000 });
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "finanza");
  await page.goto(`/budget?periodo=${s.periodId}`);
  const areaCard = page.locator(`[data-node="${s.area.name}"]`);
  await expect(page.getByRole("table")).toHaveCount(0);
  await page.locator("[data-node^='Plafond']").getByRole("button", { name: /^Scendi/ }).click();
  await expect(areaCard).toBeVisible();
  await expectNoHorizontalScroll(page);
  await areaCard.getByRole("button", { name: /^Scendi/ }).click();
  await expect(page.locator(`[data-node="${s.rep.name}"]`)).toBeVisible();
  await expectNoHorizontalScroll(page);
  await page.goto("/configurazione/organizzazione");
  await expect(page.getByRole("heading", { name: "Organizzazione", level: 1 })).toBeVisible();
  await expectNoHorizontalScroll(page);
});

test.describe("in inglese", () => {
  test.use({ locale: "en-US" });

  test("albero dei budget e organizzazione hanno i testi in inglese", async ({ page, playwright, baseURL }) => {
    const setupReq = await admin(playwright, baseURL!);
    const s = await setup(setupReq, { repAmount: 10_000_000 });
    await login(page, "finanza");
    await page.goto(`/budget?periodo=${s.periodId}`);
    await expect(page.getByRole("heading", { name: "Budget by area", level: 1 })).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "Allocated" })).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "Committed", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: `Distribute ${s.area.name}` })).toBeVisible();
    await page.getByRole("button", { name: `Distribute ${s.area.name}` }).click();
    await page.getByRole("form", { name: `Distribute ${s.area.name}` }).getByRole("button", { name: "Preview" }).click();
    await expect(page.getByText("The preview is valid")).toBeVisible();

    await login(page, "admin");
    await page.goto("/configurazione/organizzazione");
    await expect(page.getByRole("heading", { name: "Organization", level: 1 })).toBeVisible();
    await expect(page.getByRole("button", { name: "Add a top-level unit" })).toBeVisible();
  });
});
