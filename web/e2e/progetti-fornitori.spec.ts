// Progetti, passo 4: work package del fornitore con SAL e deliverable, conflitto tra due progetti
// sulla stessa applicazione, programma con numeri aggregati e roadmap. I dati si preparano via API
// come amministratore del tenant demo; date lontane perché altri test non si sovrappongano.
import { test, expect, type APIRequestContext } from "@playwright/test";
import { apiAs, call, login, uniq } from "./fixtures";

type Item = { id: string; key: string; title: string };
type Supplier = { progress: number; claims: { status: string; number: number }[] };

async function supplierProject(req: APIRequestContext) {
  const p = await call<{ key: string; title: string }>(req, "POST", "/api/v1/projects", { title: uniq("Commessa IDP"), template: "contract", no_phases: true });
  const wp = await call<Item>(req, "POST", `/api/v1/projects/${p.key}/items`, {
    kind: "work_package", title: "Integrazione IDP", delivery_mode: "supplier", estimate_effort_days: 10, start: "2031-03-03", end: "2031-03-28",
  });
  return { p, wp };
}

test("il PM registra un SAL del fornitore, lo accetta e l'avanzamento sale; consegna e accettazione di un deliverable", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const { wp } = await supplierProject(req);
  await call(req, "PUT", `/api/v1/plan-items/${wp.id}/supplier`, { contract_ref: "Ordine 31/7", contract_amount_cents: 2000000 });

  await login(page, "admin");
  await page.goto(`/e/${wp.key}?scheda=dettagli`);
  await expect(page.getByTestId("fornitore-contratto")).toBeVisible();
  // Primo SAL: dichiarato e lasciato da accettare.
  const form = page.getByTestId("sal-form");
  await form.getByTestId("sal-percent").fill("30");
  await form.getByTestId("sal-submit").click();
  const sal1 = page.getByTestId("sal-1");
  await expect(sal1).toHaveAttribute("data-status", "submitted");
  // Il form sparisce finché c'è un SAL da accettare; il PM lo accetta.
  await expect(page.getByTestId("sal-form")).toHaveCount(0);
  await sal1.getByRole("button", { name: "Accetta", exact: true }).click();
  await expect(sal1).toHaveAttribute("data-status", "accepted");
  await expect(page.getByText("dai SAL accettati").first()).toBeVisible();
  const sv = await call<Supplier>(req, "GET", `/api/v1/plan-items/${wp.id}/supplier`);
  expect(sv.progress).toBe(30);

  // Secondo SAL inserito e accettato insieme.
  await page.getByTestId("sal-percent").fill("55");
  await page.getByText("Accetta subito").click();
  await page.getByTestId("sal-submit").click();
  await expect(page.getByTestId("sal-2")).toHaveAttribute("data-status", "accepted");
  expect((await call<Supplier>(req, "GET", `/api/v1/plan-items/${wp.id}/supplier`)).progress).toBe(55);

  // Deliverable creato via API, consegnato e accettato dall'interfaccia.
  await call(req, "POST", `/api/v1/plan-items/${wp.id}/deliverables`, { title: "Documento di disegno", due: "2031-03-14" });
  await page.reload();
  const dels = page.getByTestId("fornitore-deliverable");
  await expect(dels).toContainText("Documento di disegno");
  await dels.getByRole("button", { name: "Consegna" }).first().click();
  // La consegna può chiedere una nota facoltativa: si conferma il secondo «Consegna», se c'è.
  const confirm = dels.getByRole("button", { name: "Consegna" });
  if ((await confirm.count()) > 0) await confirm.first().click();
  await dels.getByRole("button", { name: "Accetta", exact: true }).click();
  await expect(dels).toContainText("Accettato");
  await req.dispose();
});

test("due progetti sulla stessa applicazione nello stesso periodo: il piano e la Panoramica mostrano il conflitto", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const code = uniq("APP").replace(/[^A-Za-z0-9]/g, "").slice(-10).toUpperCase();
  await call(req, "POST", "/api/v1/architecture/applications/import", { csv: `codice,nome\n${code},Applicazione ${code}\n` });
  const apps = await call<{ items: { id: string; code: string }[] }>(req, "GET", "/api/v1/architecture/applications");
  const app = apps.items.find((a) => a.code === code)!;
  const a = await call<{ key: string }>(req, "POST", "/api/v1/projects", { title: uniq("Progetto A"), template: "agile", no_phases: true });
  const b = await call<{ key: string; title: string }>(req, "POST", "/api/v1/projects", { title: uniq("Progetto B"), template: "agile", no_phases: true });
  await call(req, "POST", `/api/v1/projects/${a.key}/items`, { kind: "work_package", title: "Modifica A", application_id: app.id, start: "2031-05-05", end: "2031-05-30" });
  await call(req, "POST", `/api/v1/projects/${b.key}/items`, { kind: "work_package", title: "Modifica B", application_id: app.id, start: "2031-05-19", end: "2031-06-13" });

  await login(page, "admin");
  await page.goto(`/e/${a.key}?scheda=panoramica`);
  const box = page.getByTestId("panoramica-conflitti");
  await expect(box).toContainText(b.key);
  await expect(box).toContainText(`Applicazione ${code}`);
  await page.goto(`/progetti/${a.key}/piano`);
  await expect(page.getByText("Conflitto").first()).toBeVisible();
  await req.dispose();
});

test("programma: scheda Programmi, numeri aggregati, progetti e roadmap", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const { p } = await supplierProject(req);
  const pg = await call<{ key: string; title: string }>(req, "POST", "/api/v1/programs", { title: uniq("Sicurezza dei canali") });
  await call(req, "PUT", `/api/v1/projects/${p.key}/program`, { program: pg.key });
  await call(req, "POST", `/api/v1/projects/${p.key}/items`, { kind: "milestone", title: "Collaudo IDP", date: "2031-03-31", contractual: true });

  await login(page, "admin");
  await page.goto("/progetti");
  await page.getByRole("tab", { name: "Programmi" }).click();
  const list = page.getByRole("table", { name: "Programmi" });
  await list.getByRole("link", { name: pg.key }).click();
  await expect(page.getByTestId("semaforo")).toBeVisible();
  await expect(page.getByRole("table", { name: "Progetti del programma" })).toContainText(p.title);
  const roadmap = page.getByRole("group", { name: /Roadmap/ });
  await expect(roadmap.getByRole("img", { name: /Collaudo IDP/ })).toBeVisible();
  await req.dispose();
});
