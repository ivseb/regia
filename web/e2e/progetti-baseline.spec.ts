// Progetti, passo 3: baseline fissata dal PM, Gantt con la baseline in trasparenza, change request
// nata dal superamento delle tolleranze, tolleranze in Configurazione → Progetti.
import { test, expect } from "@playwright/test";
import { apiAs, call, login, uniq } from "./fixtures";

type Item = { id: string; key: string; title: string; variance_days: number | null };
type Plan = { items: Item[]; baseline: { number: number } | null; tolerance: { days: number } };
type CR = { id: string; status: string; origin: string };

test("Piano: fissa la baseline, la milestone slitta oltre la tolleranza e nasce la change request", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const p = await call<{ key: string }>(req, "POST", "/api/v1/projects", { title: uniq("Baseline di prova"), template: "agile", no_phases: true });
  const key = p.key;
  await call(req, "POST", `/api/v1/projects/${key}/items`, { kind: "work_package", title: "Sviluppo", delivery_mode: "agile", start: "2027-03-01", end: "2027-03-12" });
  const ms = await call<Item>(req, "POST", `/api/v1/projects/${key}/items`, { kind: "milestone", title: "Rilascio", date: "2027-03-19" });

  await login(page, "admin");
  await page.goto(`/progetti/${key}/piano`);
  const panel = page.getByRole("region", { name: "Baseline e change request" });
  await expect(panel.getByText(/non ha ancora una baseline/)).toBeVisible();
  await panel.getByRole("textbox", { name: "Nota (facoltativa)" }).fill("Piano approvato");
  await panel.getByRole("button", { name: "Fissa la baseline" }).click();
  await expect(panel.getByTestId("baseline-corrente")).toContainText("baseline n. 1");
  await expect(page.getByRole("checkbox", { name: "Mostra la baseline n. 1" })).toBeChecked();

  // Lo spostamento oltre la tolleranza (dal server) apre la change request.
  const plan = await call<Plan>(req, "GET", `/api/v1/projects/${key}/plan`);
  await call(req, "PUT", `/api/v1/plan-items/${ms.id}/schedule`, { start: "2027-04-30", end: "2027-04-30" });
  const crs = (await call<{ items: CR[] }>(req, "GET", `/api/v1/projects/${key}/change-requests`)).items;
  expect(crs).toHaveLength(1);
  expect(crs[0].origin).toBe("tolerance");
  expect(plan.tolerance.days).toBeGreaterThan(0);

  await page.reload();
  const cr = panel.getByTestId("change-request");
  await expect(cr).toHaveCount(1);
  await expect(cr).toContainText("Rilascio");
  await expect(cr).toContainText(/\+\d+ g/);
  // Il progetto di prova non ha lo sponsor: la change request resta da inviare, con il motivo.
  if (crs[0].status === "draft") {
    await expect(cr).toContainText("Da inviare");
    await expect(cr.getByRole("status")).toContainText("L'approvazione non è partita");
    await cr.getByRole("button", { name: "Ritira" }).click();
    await expect(cr).toContainText("Ritirata");
  } else {
    await expect(cr).toContainText("In approvazione");
  }

  // Tabella delle milestone: data di baseline e scostamento.
  await page.goto(`/e/${key}?scheda=milestone`);
  await expect(page.getByRole("table", { name: /milestone/i }).getByText(/\+\d+ g/)).toBeVisible();
  await req.dispose();
});

test("Configurazione → Progetti: tolleranze della baseline", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const before = await call<{ tolerance_days: number; tolerance_budget_pct: number }>(req, "GET", "/api/v1/projects/settings");
  await login(page, "admin");
  await page.goto("/configurazione/progetti");
  const days = page.getByRole("spinbutton", { name: "Giorni lavorativi su milestone e fine" });
  await expect(days).toHaveValue(String(before.tolerance_days));
  await expect(page.getByRole("spinbutton", { name: "Percentuale sul budget" })).toHaveValue(String(before.tolerance_budget_pct));
  // Si salva lo stesso valore: il server condiviso dagli altri test non cambia.
  await days.fill(String(before.tolerance_days));
  await page.getByRole("button", { name: "Salva" }).click();
  await expect(page.getByRole("status").filter({ hasText: /salvat/i })).toBeVisible();
  const after = await call<{ tolerance_days: number }>(req, "GET", "/api/v1/projects/settings");
  expect(after.tolerance_days).toBe(before.tolerance_days);
  await req.dispose();
});
