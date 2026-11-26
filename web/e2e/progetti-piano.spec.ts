// «Piano» del progetto: Gantt con fasi, trascinamento con cascata, work package in linea e
// elenco per fase sul telefono.
import { test, expect } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, uniq } from "./fixtures";

type Item = { id: string; key: string; title: string; start: string | null; end: string | null; delivery_mode: string | null; kind: string };
type Plan = { phases: { id: string; name: string }[]; items: Item[] };

async function progetto(req: Parameters<typeof call>[0]) {
  const p = await call<{ key: string }>(req, "POST", "/api/v1/projects", { title: uniq("Piano di prova"), template: "mixed" });
  const plan = await call<Plan>(req, "GET", `/api/v1/projects/${p.key}/plan`);
  return { key: p.key, phase: plan.phases[0] };
}

const piano = (req: Parameters<typeof call>[0], key: string) => call<Plan>(req, "GET", `/api/v1/projects/${key}/plan`);

test("Piano: trascina il predecessore e il dipendente slitta a cascata", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const { key, phase } = await progetto(req);
  const a = await call<Item>(req, "POST", `/api/v1/projects/${key}/items`, { kind: "work_package", title: "Primo pezzo", delivery_mode: "waterfall", phase_id: phase.id, start: "2027-03-01", end: "2027-03-05" });
  const b = await call<Item>(req, "POST", `/api/v1/projects/${key}/items`, {
    kind: "work_package", title: "Secondo pezzo", delivery_mode: "waterfall", phase_id: phase.id, start: "2027-03-08", end: "2027-03-12", depends_on: [{ ref: a.key, kind: "fs" }],
  });

  await login(page, "admin");
  await page.goto(`/progetti/${key}/piano`);
  const barA = page.getByRole("button", { name: /^Primo pezzo: / });
  const barB = page.getByRole("button", { name: /^Secondo pezzo: / });
  await expect(barA).toBeVisible();
  await barA.scrollIntoViewIfNeeded();
  const labelB0 = await barB.getAttribute("aria-label");

  const box = (await barA.boundingBox())!;
  const px = box.width / 5;
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;
  await page.mouse.move(cx, cy);
  await page.mouse.down();
  for (let i = 1; i <= 10; i++) await page.mouse.move(cx + i * px, cy, { steps: 2 });
  // Anteprima: il dipendente si è già spostato durante il trascinamento.
  await expect(barB).not.toHaveAttribute("aria-label", labelB0!);
  await page.mouse.up();

  await expect
    .poll(async () => (await piano(req, key)).items.find((i) => i.id === b.id)!.start! > "2027-03-08")
    .toBeTruthy();
  const after = (await piano(req, key)).items;
  const na = after.find((i) => i.id === a.id)!;
  const nb = after.find((i) => i.id === b.id)!;
  expect(na.start! > "2027-03-01").toBeTruthy();
  expect(nb.start! > na.end!).toBeTruthy();
  await req.dispose();
});

test("Piano: aggiungi un work package a cascata in linea e un'attività sotto", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const { key, phase } = await progetto(req);
  await login(page, "admin");
  await page.goto(`/e/${key}?scheda=piano`);

  const titolo = uniq("Collaudo");
  await page.getByRole("button", { name: /Aggiungi work package/ }).first().click();
  const form = page.getByRole("group", { name: "Aggiungi work package" });
  await form.getByRole("textbox", { name: "Titolo" }).fill(titolo);
  await form.getByRole("radio", { name: "A cascata" }).click();
  await form.getByRole("button", { name: "Aggiungi", exact: true }).click();

  await expect.poll(async () => (await piano(req, key)).items.find((i) => i.title === titolo)?.delivery_mode).toBe("waterfall");
  const wp = (await piano(req, key)).items.find((i) => i.title === titolo)!;
  expect(wp.kind).toBe("work_package");

  // Sotto il work package a cascata compare «Aggiungi attività».
  await page.getByRole("button", { name: /Aggiungi attività/ }).click();
  const act = page.getByRole("group", { name: "Aggiungi attività" });
  await act.getByRole("textbox", { name: "Titolo" }).fill("Disegno del collaudo");
  await act.getByRole("button", { name: "Aggiungi", exact: true }).click();
  await expect.poll(async () => (await piano(req, key)).items.find((i) => i.title === "Disegno del collaudo")?.kind).toBe("plan_activity");

  // Milestone con data nella fase.
  await page.getByRole("button", { name: /Aggiungi milestone/ }).first().click();
  const ms = page.getByRole("group", { name: "Aggiungi milestone" });
  await ms.getByRole("textbox", { name: "Titolo" }).fill("Fine collaudo");
  await ms.getByLabel("Data").fill("2027-04-15");
  await ms.getByRole("button", { name: "Aggiungi", exact: true }).click();
  await expect.poll(async () => (await piano(req, key)).items.find((i) => i.title === "Fine collaudo")?.start).toBe("2027-04-15");
  void phase;
  await req.dispose();
});

test("Piano: sul telefono è un elenco per fase senza scorrimento orizzontale", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const { key, phase } = await progetto(req);
  await call(req, "POST", `/api/v1/projects/${key}/items`, { kind: "work_package", title: "Pezzo sul telefono", delivery_mode: "agile", phase_id: phase.id, start: "2027-03-01", end: "2027-03-12" });
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "admin");
  await page.goto(`/e/${key}?scheda=piano`);
  await expect(page.getByRole("heading", { name: phase.name })).toBeVisible();
  const voce = page.getByRole("link", { name: /Pezzo sul telefono/ });
  await expect(voce).toBeVisible();
  await expect(voce).toContainText("dal");
  await expect(page.getByRole("button", { name: /Aggiungi work package/ })).toHaveCount(0);
  await expectNoHorizontalScroll(page);
  await page.goto(`/progetti/${key}/piano`);
  await expect(page.getByRole("link", { name: /Pezzo sul telefono/ })).toBeVisible();
  await expectNoHorizontalScroll(page);
  await req.dispose();
});
