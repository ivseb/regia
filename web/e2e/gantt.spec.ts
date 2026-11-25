// Gantt: si trascina la barra di un impatto e quello che dipende da lui slitta a cascata,
// sia nell'anteprima sia sul server.
import { test, expect } from "@playwright/test";
import { advance, apiAs, call, login, newRequest, uniq } from "./fixtures";

type Task = { id: string; key: string; application: string; start: string | null; end: string | null };

test("Gantt: trascina una barra e verifica la cascata", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const { id, key } = await newRequest(req, uniq("Accesso con SPID all'area clienti"));
  await req.dispose();
  const { req: dm } = await apiAs(playwright, baseURL!, "dm");
  await advance(dm, id, "Triage", "Valutazione");
  const app = async (code: string) => (await call(dm, "GET", `/api/v1/architecture/applications?q=${code}`)).items[0].id as string;
  await call(dm, "POST", `/api/v1/demand/requests/${id}/impacts`, { application_id: await app("IAM"), change_kind: "change", estimate_effort_days: 4 });
  await call(dm, "POST", `/api/v1/demand/requests/${id}/impacts`, { application_id: await app("API-CLI"), change_kind: "change", estimate_effort_days: 6 });
  const plan0 = await call<{ tasks: Task[] }>(dm, "GET", `/api/v1/demand/requests/${id}/plan`);
  const iam = plan0.tasks.find((t) => t.application === "IAM / SSO")!;
  const api = plan0.tasks.find((t) => t.application === "Backend API clienti")!;
  await call(dm, "PATCH", `/api/v1/impacts/${api.id}`, { depends_on: [iam.key] });
  await call(dm, "POST", `/api/v1/demand/requests/${id}/plan/auto`, { replan: true });
  const before = await call<{ tasks: Task[] }>(dm, "GET", `/api/v1/demand/requests/${id}/plan`);
  const b = (x: Task) => before.tasks.find((t) => t.id === x.id)!;
  expect(b(api).start! > b(iam).end!).toBeTruthy();

  await login(page, "dm");
  await page.goto(`/richieste/${key}/piano`);
  const iamBar = page.getByRole("button", { name: /^IAM \/ SSO: / });
  const apiBar = page.getByRole("button", { name: /^Backend API clienti: / });
  await expect(iamBar).toBeVisible();
  await expect(apiBar).toBeVisible();
  const apiLabel0 = await apiBar.getAttribute("aria-label");

  // Trascinamento dal centro della barra di 10 giorni (in scala "settimane" un giorno sono px fissi:
  // si ricava dalla larghezza della barra e dai suoi giorni di calendario).
  const box = (await iamBar.boundingBox())!;
  const days = (Date.parse(b(iam).end!) - Date.parse(b(iam).start!)) / 86_400_000 + 1;
  const px = box.width / days;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  for (let i = 1; i <= 10; i++) await page.mouse.move(box.x + box.width / 2 + i * px, box.y + box.height / 2, { steps: 2 });
  // Anteprima: durante il trascinamento la barra dipendente si è già spostata.
  await expect(apiBar).not.toHaveAttribute("aria-label", apiLabel0!);
  await page.mouse.up();

  // Sul server: l'impatto trascinato e quello dipendente partono più tardi, e la dipendenza tiene.
  await expect
    .poll(async () => {
      const p = await call<{ tasks: Task[] }>(dm, "GET", `/api/v1/demand/requests/${id}/plan`);
      return p.tasks.find((t) => t.id === api.id)!.start! > b(api).start!;
    })
    .toBeTruthy();
  const after = await call<{ tasks: Task[] }>(dm, "GET", `/api/v1/demand/requests/${id}/plan`);
  const a = (x: Task) => after.tasks.find((t) => t.id === x.id)!;
  expect(a(iam).start! > b(iam).start!).toBeTruthy();
  expect(a(api).start! > a(iam).end!).toBeTruthy();
  await expect(apiBar).not.toHaveAttribute("aria-label", apiLabel0!);
  await dm.dispose();
});
