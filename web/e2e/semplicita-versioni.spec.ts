// «Tutto ciò che si configura evolve nel tempo»: pubblicazione di un processo con data di entrata in vigore
// (modifica → anteprima → pubblica con data → transizione), guide allegate visibili nel percorso, 375 px.
import { test, expect, type Page } from "@playwright/test";
import { call, expectNoHorizontalScroll, login, uniq } from "./fixtures";

const MOBILE = { width: 375, height: 812 };

/** Un processo nuovo copiato dallo standard, già pubblicato (v1) e con una bozza uguale alla corrente (v2). */
async function processoConBozza(page: Page, priority: number) {
  const name = uniq("Versioni");
  const key = name.toLowerCase().replaceAll(/[^a-z0-9]+/g, "_");
  await call(page.request, "POST", "/api/v1/processes", { key, name, type: "business_request", priority, copy_from: "demand_standard" });
  await call(page.request, "POST", `/api/v1/processes/${key}/publish`, {});
  const p = await call<{ current: unknown }>(page.request, "GET", `/api/v1/processes/${key}`);
  await call(page.request, "PUT", `/api/v1/processes/${key}/draft`, { definition: p.current, notes: "Nuovo standard" });
  return { key, name };
}

function tomorrow() {
  const d = new Date(Date.now() + 36 * 3600 * 1000);
  return d.toISOString().slice(0, 10);
}

test("pubblica con una data: passi, anteprima della transizione, versione programmata", async ({ page }) => {
  await login(page, "admin");
  const { key } = await processoConBozza(page, 920);
  await page.goto(`/processi/${key}`);
  const flow = page.getByRole("region", { name: "Pubblica con una data" });
  await expect(flow).toBeVisible();
  await expect(flow.getByRole("list", { name: "Passi della pubblicazione" }).locator("li")).toHaveCount(4);
  await flow.getByRole("button", { name: "Vedi l'anteprima" }).click();
  await flow.getByLabel("Da questa data").check();
  await flow.getByLabel("Entra in vigore il").fill(tomorrow());
  await expect(flow.getByText(/entra in vigore il /)).toBeVisible();
  await expect(flow.getByText(/Fino ad allora vale la v1/)).toBeVisible();
  await expect(flow.getByText(/Niente da spostare/)).toBeVisible();
  await flow.getByRole("button", { name: "Avanti: pubblica" }).click();
  await flow.getByRole("button", { name: "Pubblica con questa data" }).click();
  await expect(flow.getByRole("status")).toContainText("Versione programmata");
  // La v1 resta in vigore, la v2 è programmata.
  const versioni = page.getByRole("table").first();
  await expect(page.getByText("in vigore", { exact: true }).first()).toBeVisible();
  await expect(page.getByText(/^programmata dal /).first()).toBeVisible();
  void versioni;
  const p = await call<{ versions: { number: number; lifecycle: string }[] }>(page.request, "GET", `/api/v1/processes/${key}`);
  expect(p.versions.find((v) => v.number === 2)?.lifecycle).toBe("scheduled");
  expect(p.versions.find((v) => v.number === 1)?.lifecycle).toBe("in_force");
});

test("guide allegate: si aggiungono dalla pagina del processo e si vedono nel percorso della richiesta", async ({ page }) => {
  await login(page, "admin");
  const { key } = await processoConBozza(page, 930);
  await page.goto(`/processi/${key}`);
  const guide = page.getByRole("region", { name: "Guide allegate" });
  await expect(guide.getByText("Nessuna guida allegata.")).toBeVisible();
  const title = uniq("Linee guida");
  await guide.getByLabel("Titolo").fill(title);
  await guide.getByLabel("Destinazione").fill("https://esempio.test/guida");
  await guide.getByRole("button", { name: "Allega la guida" }).click();
  await expect(guide.getByRole("link", { name: title })).toBeVisible();
  // Un indirizzo non ammesso viene rifiutato con un messaggio.
  await guide.getByLabel("Titolo").fill("Cattiva");
  await guide.getByLabel("Destinazione").fill("javascript:alert(1)");
  await guide.getByRole("button", { name: "Allega la guida" }).click();
  await expect(guide.getByRole("alert")).toBeVisible();

  // Una guida del processo standard compare nel percorso di una richiesta che lo usa.
  const shared = uniq("Guida del processo");
  const g = await call<{ id: string }>(page.request, "POST", "/api/v1/processes/demand_standard/guides", { kind: "link", title: shared, target: "https://esempio.test/standard" });
  await page.goto("/e/DEM-5?scheda=attivita");
  const panel = page.getByRole("region", { name: "Percorso", exact: true });
  await expect(panel.getByRole("link", { name: shared })).toBeVisible();
  await call(page.request, "DELETE", `/api/v1/processes/demand_standard/guides/${g.id}`);
});

test("telefono a 375 px: pagina del processo con la pubblicazione e le guide", async ({ page }) => {
  await page.setViewportSize(MOBILE);
  await login(page, "admin");
  const { key } = await processoConBozza(page, 940);
  await page.goto(`/processi/${key}`);
  const flow = page.getByRole("region", { name: "Pubblica con una data" });
  await flow.getByRole("button", { name: "Vedi l'anteprima" }).click();
  await expect(flow.getByText(/entra in vigore subito/)).toBeVisible();
  await expect(page.getByRole("region", { name: "Guide allegate" })).toBeVisible();
  await expectNoHorizontalScroll(page);
});
