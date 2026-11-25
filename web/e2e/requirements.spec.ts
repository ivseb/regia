// Requisiti: il verificatore trova il requisito pronto nell'inbox e lo verifica con un'evidenza;
// un altro requisito va in deroga, approvata da chi prevede la politica.
import { test, expect, type APIRequestContext } from "@playwright/test";
import { advance, apiAs, call, login, newRequest, uniq } from "./fixtures";

/** Richiesta con un impatto sul Backend API clienti e un requisito di progetto verificato dal team Sicurezza. */
async function setup(admin: APIRequestContext, dm: APIRequestContext, title: string) {
  const { id, key } = await newRequest(dm, uniq("Firma remota dei contratti"));
  await advance(dm, id, "Triage", "Valutazione");
  const app = (await call(dm, "GET", "/api/v1/architecture/applications?q=API-CLI")).items[0].id;
  await call(dm, "POST", `/api/v1/demand/requests/${id}/impacts`, { application_id: app, change_kind: "change", estimate_effort_days: 5 });
  await call(dm, "POST", `/api/v1/elements/${id}/requirements`, {
    title, text: "Le sessioni scadono dopo 15 minuti di inattività.", category: "Sicurezza", scope: "project",
    verifier_team: "sicurezza", verification_criterion: "Prova manuale della scadenza della sessione",
  });
  const m = await call(dm, "GET", `/api/v1/elements/${id}/requirements`);
  const row = m.rows.find((r: { title: string }) => r.title === title);
  const r = await call(admin, "GET", `/api/v1/requirements/${row.key}`);
  return { key, reqKey: row.key as string, reqId: r.requirement.id as string, impact: r.cells[0].impact_key as string };
}

test("requisito: verifica con evidenza dall'inbox del verificatore", async ({ page, playwright, baseURL }) => {
  const admin = await apiAs(playwright, baseURL!, "admin");
  const dm = await apiAs(playwright, baseURL!, "dm");
  const title = uniq("Scadenza delle sessioni inattive");
  const { reqKey, reqId, impact } = await setup(admin.req, dm.req, title);
  await advance(admin.req, reqId, "Accettato", "In realizzazione");
  await call(admin.req, "POST", `/api/v1/requirements/${reqKey}/cells/${impact}/ready`, { note: "Timeout configurato nel gateway" });

  await login(page, "sicurezza");
  await page.goto("/requisiti");
  await expect(page.getByRole("heading", { name: "Da verificare" })).toBeVisible();
  const item = page.getByRole("link", { name: new RegExp(`${reqKey} .*${title}`) });
  await expect(item).toBeVisible();
  await item.click();

  const cells = page.getByRole("region", { name: "Applicazioni" });
  await cells.getByRole("button", { name: "Verifica", exact: true }).click();
  await page.getByLabel("Nota dell'evidenza").fill("Sessione chiusa dopo 15 minuti, provato su ambiente di collaudo.");
  await page.getByLabel("Link alle evidenze (uno per riga)").fill("https://collaudo.example.test/report/sessioni");
  await page.getByRole("button", { name: "Firma e registra" }).click();
  await expect(cells.getByText(/Verificato da Giulia Sala/)).toBeVisible();
  const r = await call(admin.req, "GET", `/api/v1/requirements/${reqKey}`);
  expect(r.cells[0].status).toBe("verified");

  // Non è più nell'inbox.
  await page.goto("/requisiti");
  await expect(page.getByRole("heading", { name: "Da verificare" })).toBeVisible();
  await expect(page.getByRole("link", { name: new RegExp(reqKey + " ") })).toHaveCount(0);
  await admin.req.dispose();
  await dm.req.dispose();
});

test("requisito: deroga chiesta dall'interfaccia e approvata", async ({ page, playwright, baseURL }) => {
  const admin = await apiAs(playwright, baseURL!, "admin");
  const dm = await apiAs(playwright, baseURL!, "dm");
  const title = uniq("Blocco dopo cinque tentativi falliti");
  const { reqKey, reqId } = await setup(admin.req, dm.req, title);
  await advance(admin.req, reqId, "Accettato");

  await login(page, "admin");
  await page.goto(`/e/${reqKey}?scheda=dettagli`);
  const cells = page.getByRole("region", { name: "Applicazioni" });
  await cells.getByRole("button", { name: "Chiedi una deroga" }).click();
  await page.getByLabel("Motivazione").fill("Il blocco arriva con la migrazione al nuovo IdP, prevista a marzo.");
  await page.getByLabel("Misure compensative").fill("Allarme sul SIEM dopo cinque tentativi falliti.");
  await page.getByRole("button", { name: "Chiedi l'approvazione" }).click();
  const deroghe = page.getByRole("region", { name: "Deroghe" });
  const ex = deroghe.getByRole("link", { name: /DEM-\d+/ }).first();
  await expect(ex).toBeVisible();
  const exKey = (await ex.innerText()).match(/DEM-\d+/)![0];

  // Approva chi prevede la politica (team Sicurezza), mai chi la chiede.
  const sec = await apiAs(playwright, baseURL!, "sicurezza");
  const d = await call(sec.req, "GET", `/api/v1/elements/${exKey}`);
  const step = d.approvals.find((a: { status: string }) => a.status === "pending").steps.find((s: { decision: string }) => s.decision === "pending");
  await call(sec.req, "POST", `/api/v1/approvals/steps/${step.id}/decision`, { decision: "approved", comment: "Ok con l'allarme sul SIEM." });
  await sec.req.dispose();

  await page.reload();
  await expect(cells.getByText(new RegExp(`In deroga \\(${exKey}\\) fino al`))).toBeVisible();
  await admin.req.dispose();
  await dm.req.dispose();
});
