// Progetti (replica/progetti.md): richiesta approvata che diventa progetto, Panoramica del PM, tabella dei
// work package, «Nuovo progetto» misto e telefono senza scorrimento orizzontale.
import { test, expect } from "@playwright/test";
import { advance, apiAs, call, expectNoHorizontalScroll, login, newRequest, uniq } from "./fixtures";

/** Richiesta con due impatti, approvata dallo sponsor: nel tenant demo diventa da sola un progetto. */
async function approvedRequestWithImpacts(playwright: Parameters<typeof apiAs>[0], baseURL: string, label: string) {
  const { req } = await apiAs(playwright, baseURL, "richiedente");
  const title = uniq(label);
  const { id, key } = await newRequest(req, title);
  await req.dispose();
  const dm = await apiAs(playwright, baseURL, "dm");
  await advance(dm.req, id, "Triage", "Valutazione");
  await call(dm.req, "PATCH", `/api/v1/demand/requests/${id}`, { estimate_low_cents: 2_000_000, estimate_high_cents: 3_000_000 });
  await advance(dm.req, id, "Prioritizzazione");
  // Gli impatti si aggiungono dopo le valutazioni (che con impatti aperti chiedono esiti obbligatori).
  const app = async (code: string) => (await call(dm.req, "GET", `/api/v1/architecture/applications?q=${code}`)).items[0].id as string;
  await call(dm.req, "POST", `/api/v1/demand/requests/${id}/impacts`, { application_id: await app("IAM"), change_kind: "change", estimate_effort_days: 4 });
  await call(dm.req, "POST", `/api/v1/demand/requests/${id}/impacts`, { application_id: await app("API-CLI"), change_kind: "change", estimate_effort_days: 6 });
  const el = await call(dm.req, "GET", `/api/v1/elements/${id}`);
  await call(dm.req, "PATCH", `/api/v1/elements/${id}`, { version: el.element.version, fields: { data_golive: "2027-05-31" } });
  await advance(dm.req, id, "Approvazione");
  await dm.req.dispose();
  const sp = await apiAs(playwright, baseURL, "sponsor");
  const inbox = await call<{ items: { step_id: string; element_key: string }[] }>(sp.req, "GET", "/api/v1/approvals/inbox");
  const step = inbox.items.find((i) => i.element_key === key)!;
  expect(step, "decisione dello sponsor in attesa").toBeTruthy();
  await call(sp.req, "POST", `/api/v1/approvals/steps/${step.step_id}/decision`, { decision: "approved", comment: "Approvata dai test." });
  await sp.req.dispose();
  return { id, key, title };
}

test("richiesta approvata: il progetto, la Panoramica e i work package agili", async ({ page, playwright, baseURL }) => {
  const { key } = await approvedRequestWithImpacts(playwright, baseURL!, "MFA sui canali digitali");
  await login(page, "dm");
  await page.goto(`/e/${key}?scheda=piano`);
  const banner = page.getByTestId("progetto-della-richiesta");
  await expect(banner).toContainText(/Questa richiesta è il progetto PRJ-\d+/);
  await banner.getByRole("link", { name: "Apri il piano del progetto" }).click();

  // Il progetto si apre sulla Panoramica: 2 work package e l'avanzamento.
  await page.getByRole("tab", { name: "Panoramica" }).click();
  await expect(page.getByTestId("semaforo")).toBeVisible();
  await expect(page.getByText("0 di 2 work package completati")).toBeVisible();
  await expect(page.getByRole("progressbar", { name: /Avanzamento \d+%/ }).first()).toBeVisible();
  await expect(page.getByText("Baseline non ancora fissata")).toBeVisible();
  await expect(page.getByText("Nessuna change request aperta.")).toBeVisible();

  await page.getByRole("tab", { name: "Work package" }).click();
  const rows = page.getByRole("table", { name: "Work package del progetto" }).getByRole("row");
  await expect(rows).toHaveCount(3); // intestazione + 2
  await expect(rows.filter({ hasText: "Agile" })).toHaveCount(2);
  await page.getByRole("tab", { name: "Milestone" }).click();
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
});

test("Nuovo progetto misto: Panoramica vuota con le fasi", async ({ page }) => {
  await login(page, "dm");
  await page.goto("/progetti/nuovo");
  const title = uniq("Archivio documentale");
  await page.getByLabel("Titolo").fill(title);
  await page.getByRole("radio", { name: /Misto/ }).check();
  await page.getByRole("button", { name: "Crea il progetto" }).click();
  await expect(page.getByRole("heading", { name: title, level: 1 })).toBeVisible();
  await expect(page.getByText("Il piano non ha ancora voci")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Fasi" })).toBeVisible();

  await page.goto("/progetti");
  await expect(page.getByRole("table", { name: "Progetti" }).getByText(title)).toBeVisible();
});

test("telefono: Panoramica e tabella senza scorrimento orizzontale", async ({ page, playwright, baseURL }) => {
  const { key } = await approvedRequestWithImpacts(playwright, baseURL!, "Firma remota dei contratti");
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "dm");
  await page.goto(`/e/${key}?scheda=piano`);
  await page.getByRole("link", { name: "Apri il piano del progetto" }).click();
  await page.getByRole("tab", { name: "Panoramica" }).click();
  await expect(page.getByTestId("semaforo")).toBeVisible();
  await expectNoHorizontalScroll(page);
  await page.getByRole("tab", { name: "Work package" }).click();
  await expect(page.getByRole("list", { name: "Work package del progetto" })).toBeVisible();
  await expectNoHorizontalScroll(page);
  await page.goto("/progetti");
  await expect(page.getByRole("list", { name: "Progetti" })).toBeVisible();
  await expectNoHorizontalScroll(page);
});
