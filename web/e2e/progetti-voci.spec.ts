// Progetti: scheda «Dettagli» delle voci di piano (avanzamento dichiarato, dipendenze) e
// Configurazione → Progetti. I dati si preparano via API come amministratore del tenant demo.
import { test, expect, type APIRequestContext } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, scegli, uniq } from "./fixtures";

type Item = { id: string; key: string; title: string };

/** Progetto a cascata con un work package a cascata e due attività. */
async function setup(req: APIRequestContext) {
  const project = await call<{ id: string; key: string }>(req, "POST", "/api/v1/projects", { title: uniq("Progetto voci"), template: "waterfall" });
  const wp = await call<Item>(req, "POST", `/api/v1/projects/${project.key}/items`, { kind: "work_package", title: "Collaudo", delivery_mode: "waterfall" });
  const a1 = await call<Item>(req, "POST", `/api/v1/projects/${project.key}/items`, { kind: "plan_activity", title: "Piano di prova", parent_id: wp.key, estimate_effort_days: 2 });
  const a2 = await call<Item>(req, "POST", `/api/v1/projects/${project.key}/items`, { kind: "plan_activity", title: "Esecuzione", parent_id: wp.key, estimate_effort_days: 2 });
  return { project, wp, a1, a2 };
}

test("l'attività registra l'avanzamento con una nota e il work package lo mostra dalle attività", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const s = await setup(req);
  await login(page, "admin");
  await page.goto(`/e/${s.a1.key}?scheda=dettagli`);
  await expect(page.getByRole("heading", { name: "Avanzamento dichiarato" })).toBeVisible();
  await page.getByLabel("Percentuale").fill("40");
  await page.getByPlaceholder("Che cosa è cambiato").fill("Piano scritto");
  await page.getByRole("button", { name: "Registra l'avanzamento" }).click();
  const storico = page.getByRole("list", { name: "Storico dell'avanzamento" });
  await expect(storico).toContainText("40%");
  await expect(storico).toContainText("Piano scritto");

  await page.goto(`/e/${s.wp.key}?scheda=dettagli`);
  await expect(page.getByRole("heading", { name: "Attività", exact: true })).toBeVisible();
  await expect(page.getByText("dalle attività")).toBeVisible();
  // Due attività da 2 giorni, una al 40%: il work package è al 20%.
  await expect(page.getByLabel("Avanzamento", { exact: true })).toContainText("20%");
  await expect(page.getByRole("region", { name: "Attività", exact: true }).getByRole("link", { name: /Piano di prova/ })).toBeVisible();
});

test("aggiunge una dipendenza inizio→inizio con ritardo 2 e viene salvata", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const s = await setup(req);
  await login(page, "admin");
  await page.goto(`/e/${s.a2.key}?scheda=dettagli`);
  await expect(page.getByRole("heading", { name: "Dipendenze" })).toBeVisible();
  await scegli(page.getByRole("combobox", { name: /Voce da cui dipende/ }), new RegExp(s.a1.key));
  await scegli(page.getByRole("combobox", { name: /^Tipo/ }), "Inizio → inizio");
  await page.getByLabel("Ritardo (giorni lavorativi)").fill("2");
  await page.getByRole("region", { name: "Dipendenze" }).getByRole("button", { name: "Aggiungi", exact: true }).click();
  await page.getByRole("button", { name: "Salva le dipendenze" }).click();
  await expect(page.getByRole("button", { name: "Salva le dipendenze" })).toBeDisabled();
  await expect.poll(async () => (await call<{ deps: { id: string; kind: string; lag: number }[] }>(req, "GET", `/api/v1/plan-items/${s.a2.key}`)).deps)
    .toEqual([{ id: s.a1.id, kind: "ss", lag: 2 }]);
});

test("Configurazione → Progetti: un ruolo obbligatorio si salva e resta dopo il ricaricamento", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const before = await call<{ roles: { key: string; required: boolean }[] }>(req, "GET", "/api/v1/projects/settings");
  await login(page, "admin");
  try {
    await page.goto("/configurazione/progetti");
    await expect(page.getByRole("heading", { level: 1, name: "Progetti" })).toBeVisible();
    const riga = page.getByRole("listitem").filter({ has: page.getByRole("checkbox", { name: "Obbligatorio" }) }).first();
    const box = riga.getByRole("checkbox", { name: "Obbligatorio" });
    const era = await box.isChecked();
    await box.setChecked(!era);
    await page.getByRole("button", { name: "Salva", exact: true }).click();
    await expect(page.getByRole("status").filter({ hasText: "Salvato" })).toBeVisible();
    await page.reload();
    await expect(page.getByRole("listitem").filter({ has: page.getByRole("checkbox", { name: "Obbligatorio" }) }).first().getByRole("checkbox", { name: "Obbligatorio" })).toBeChecked({ checked: !era });
  } finally {
    await call(req, "PUT", "/api/v1/projects/settings", { roles: before.roles });
  }
  const after = await call<{ roles: { key: string; required: boolean }[] }>(req, "GET", "/api/v1/projects/settings");
  expect(after.roles.map((r) => [r.key, r.required])).toEqual(before.roles.map((r) => [r.key, r.required]));
});

test("a 375 px la scheda di un'attività e quella del work package non scorrono in orizzontale", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const s = await setup(req);
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "admin");
  for (const k of [s.a1.key, s.wp.key]) {
    await page.goto(`/e/${k}?scheda=dettagli`);
    await expect(page.getByRole("heading", { name: "Dipendenze" })).toBeVisible();
    await page.waitForLoadState("networkidle");
    await expectNoHorizontalScroll(page);
  }
  await page.goto("/configurazione/progetti");
  await expect(page.getByRole("heading", { level: 1, name: "Progetti" })).toBeVisible();
  await expectNoHorizontalScroll(page);
});
