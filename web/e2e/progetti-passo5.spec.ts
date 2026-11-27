// Progetti, passo 5: status report del progetto dalla scheda «Stato» con lo storico in Panoramica,
// budget del progetto in Panoramica, tolleranze del progetto e voce aggiunta dopo la baseline (change
// request di perimetro), «In arrivo dai progetti» nel backlog del team. I dati si preparano via API come
// amministratore del tenant demo.
import { test, expect } from "@playwright/test";
import { apiAs, call, login, uniq } from "./fixtures";

type Item = { id: string; key: string };
const day = (offset: number) => new Date(Date.now() + offset * 86_400_000).toISOString().slice(0, 10);

test("il PM scrive lo status report del progetto partendo dai numeri; la Panoramica ne mostra lo storico", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const p = await call<{ key: string }>(req, "POST", "/api/v1/projects", { title: uniq("Report progetto"), template: "agile", no_phases: true });
  await call(req, "POST", `/api/v1/projects/${p.key}/items`, { kind: "work_package", title: "Sviluppo", delivery_mode: "waterfall", estimate_effort_days: 5, start: "2031-09-01", end: "2031-09-12" });

  await login(page, "admin");
  await page.goto(`/e/${p.key}?scheda=stato`);
  await page.getByRole("button", { name: "Nuovo status report" }).click();
  const data = page.getByTestId("report-dati-progetto").first();
  await expect(data).toContainText("Work package chiusi");
  await expect(data).toContainText("0/1");
  const summary = page.getByRole("textbox", { name: /^Sintesi/ });
  await expect(summary).toHaveValue(/work package chiusi/);
  await summary.fill("Avvio regolare, nessun problema aperto.");
  await page.getByRole("button", { name: "Invia", exact: true }).click();
  // Inviato: la bozza sparisce e torna «Nuovo status report».
  await expect(page.getByRole("button", { name: "Nuovo status report" })).toBeVisible();
  await expect(page.getByText("Avvio regolare, nessun problema aperto.").first()).toBeVisible();

  await page.goto(`/e/${p.key}?scheda=panoramica`);
  const box = page.getByTestId("panoramica-report");
  await expect(box).toContainText("Avvio regolare");
  await expect(box).toContainText("da vedere");
  await expect(page.getByTestId("panoramica-budget")).toContainText("Budget e costi");
  await req.dispose();
});

test("tolleranze del progetto dall'interfaccia; un work package aggiunto dopo la baseline apre la change request di perimetro", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const p = await call<{ key: string }>(req, "POST", "/api/v1/projects", { title: uniq("Perimetro"), template: "agile", no_phases: true });
  await call(req, "POST", `/api/v1/projects/${p.key}/items`, { kind: "work_package", title: "Analisi", start: "2031-10-06", end: "2031-10-17" });
  await call(req, "POST", `/api/v1/projects/${p.key}/baselines`, { note: "e2e" });

  await login(page, "admin");
  await page.goto(`/progetti/${p.key}/piano`);
  const tol = page.getByTestId("tolleranze-progetto");
  await expect(tol).toContainText("tolleranze dell'organizzazione");
  await tol.getByRole("button", { name: "Cambia le tolleranze del progetto" }).click();
  const form = page.getByRole("form", { name: "Tolleranze del progetto" });
  await form.getByLabel("Giorni lavorativi su milestone e fine").fill("8");
  await form.getByRole("button", { name: "Salva le tolleranze" }).click();
  await expect(page.getByTestId("tolleranze-progetto")).toContainText("tolleranze proprie");
  await expect(page.getByText("+8 giorni lavorativi").first()).toBeVisible();

  const wp = await call<Item>(req, "POST", `/api/v1/projects/${p.key}/items`, { kind: "work_package", title: "Formazione", start: "2031-10-20", end: "2031-10-24" });
  await page.reload();
  const cr = page.getByTestId("change-request").first();
  await expect(cr).toContainText("Variazione del perimetro");
  await expect(cr.locator('[data-kind="scope_added"]')).toContainText("aggiunta dopo la baseline");

  await page.goto(`/e/${p.key}?scheda=panoramica`);
  await expect(page.getByTestId("panoramica-perimetro")).toContainText(wp.key);
  await req.dispose();
});

test("il team vede nel backlog il lavoro che i progetti hanno pianificato su di lui", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  // Il team nuovo riceve subito il suo spazio di consegna (board e backlog).
  const name = uniq("Team arrivi");
  await call(req, "POST", "/api/v1/teams", { name });
  const teams = await call<{ items: { id: string; name: string; space_key: string | null }[] }>(req, "GET", "/api/v1/work/teams");
  const team = teams.items.find((x) => x.name === name);
  expect(team?.space_key, JSON.stringify(team)).toBeTruthy();
  const p = await call<{ key: string }>(req, "POST", "/api/v1/projects", { title: uniq("In arrivo"), template: "agile", no_phases: true });
  const wp = await call<Item>(req, "POST", `/api/v1/projects/${p.key}/items`, {
    kind: "work_package", title: "Nuovo canale", delivery_mode: "agile", team_id: team!.id, estimate_effort_days: 4, start: day(7), end: day(14),
  });

  await login(page, "admin");
  await page.goto(`/delivery/backlog?spazio=${team!.space_key}`);
  const box = page.getByTestId("in-arrivo");
  await expect(box).toContainText(wp.key);
  await expect(box).toContainText(p.key);
  await expect(box).toContainText("da accettare");
  await req.dispose();
});
