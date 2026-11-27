// Timesheet deciso in linea da «Da fare»: chi approva non apre la pagina del tempo. Approvare, respingere
// con il motivo obbligatorio, la propria settimana senza pulsanti, e a 375 px senza scorrere di lato.
import { test, expect } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, uniq } from "./fixtures";

/** Lunedì di una settimana passata scelta a caso (i test girano in parallelo sullo stesso server). */
function pastWeek() {
  const d = new Date();
  d.setDate(d.getDate() - ((d.getDay() + 6) % 7) - 7 * (30 + Math.floor(Math.random() * 300)));
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

async function prepara(playwright: Parameters<typeof apiAs>[0], baseURL: string) {
  const admin = await apiAs(playwright, baseURL, "admin");
  await call(admin.req, "PUT", "/api/v1/work/time/settings", { require_approval: true, approver_mode: "admin" });
  const team = uniq("Team timesheet inbox");
  await call(admin.req, "POST", "/api/v1/teams", { name: team });
  const spaces = await call(admin.req, "GET", "/api/v1/work/spaces");
  const space = spaces.items.find((s: { team: string }) => s.team === team).key as string;
  const item = await call(admin.req, "POST", `/api/v1/work/spaces/${space}/items`, { type: "story", title: "Lavoro del timesheet in linea" });
  const dev = await apiAs(playwright, baseURL, "dev");
  const week = pastWeek();
  await call(dev.req, "POST", `/api/v1/work/elements/${item.key}/time`, { duration: "2h", date: week });
  await call(dev.req, "POST", "/api/v1/work/timesheet/submit", { week });
  return { admin, dev, week };
}

/** La riga della settimana in «Da fare»: la distingue il link «Apri il timesheet» con la settimana scelta. */
function riga(page: import("@playwright/test").Page, week: string) {
  return page.getByRole("listitem").filter({ has: page.locator(`a[href*="settimana=${week}"]`) }).filter({ hasText: /Anna Moretti/ });
}

test("l'approvatore approva da «Da fare» senza aprire il tempo", async ({ page, playwright, baseURL }) => {
  const { admin, dev, week } = await prepara(playwright, baseURL!);
  await login(page, "admin");
  await page.goto("/da-fare");
  const r = riga(page, week);
  await expect(r).toBeVisible();
  await expect(r).toContainText("2h");
  await r.getByRole("button", { name: "Approva" }).click();
  await expect(page.getByRole("status").filter({ hasText: /Anna Moretti approvata|approvata/ })).toBeVisible();
  await expect(r).toHaveCount(0);
  await expect(page).toHaveURL(/\/da-fare/);
  const dopo = await call(dev.req, "GET", `/api/v1/work/timesheet?week=${week}`);
  expect(dopo.status).toBe("approved");
  await admin.req.dispose();
  await dev.req.dispose();
});

test("respingere da «Da fare» vuole il motivo, e la settimana torna modificabile", async ({ page, playwright, baseURL }) => {
  const { admin, dev, week } = await prepara(playwright, baseURL!);
  await login(page, "admin");
  await page.goto("/da-fare");
  const r = riga(page, week);
  await r.getByRole("button", { name: "Respingi" }).click();
  // Motivo vuoto: non parte, resta il campo con l'indicazione.
  await r.getByRole("button", { name: "Conferma il rifiuto" }).click();
  await expect(r.getByText("Scrivi il motivo del rifiuto.")).toBeVisible();
  expect((await call(dev.req, "GET", `/api/v1/work/timesheet?week=${week}`)).status).toBe("submitted");
  // Annulla riporta ai pulsanti.
  await r.getByRole("button", { name: "Annulla" }).click();
  await expect(r.getByRole("button", { name: "Approva" })).toBeVisible();
  await r.getByRole("button", { name: "Respingi" }).click();
  await r.getByLabel("Motivo del rifiuto").fill("Manca il giovedì");
  await r.getByRole("button", { name: "Conferma il rifiuto" }).click();
  await expect(page.getByRole("status").filter({ hasText: /respinta/ })).toBeVisible();
  await expect(r).toHaveCount(0);
  const dopo = await call(dev.req, "GET", `/api/v1/work/timesheet?week=${week}`);
  expect(dopo.status).toBe("rejected");
  expect(dopo.editable).toBe(true);
  const avvisi = await call(dev.req, "GET", "/api/v1/notifications");
  expect(avvisi.items.some((n: { kind: string; payload: { event?: string; comment?: string } }) => n.kind === "timesheet" && n.payload.event === "rejected" && n.payload.comment === "Manca il giovedì")).toBe(true);
  await admin.req.dispose();
  await dev.req.dispose();
});

test("a 375 px i pulsanti sono alti 44 px e la pagina non scorre di lato", async ({ page, playwright, baseURL }) => {
  const { admin, dev, week } = await prepara(playwright, baseURL!);
  await admin.req.dispose();
  await dev.req.dispose();
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "admin");
  await page.goto("/da-fare");
  const r = riga(page, week);
  await expect(r).toBeVisible();
  for (const nome of ["Approva", "Respingi"]) {
    const box = await r.getByRole("button", { name: nome }).boundingBox();
    expect(box!.height).toBeGreaterThanOrEqual(43.5);
  }
  await r.getByRole("button", { name: "Respingi" }).click();
  await expect(r.getByLabel("Motivo del rifiuto")).toBeVisible();
  await expectNoHorizontalScroll(page);
});

test("chi ha inviato la settimana non vede i pulsanti di decisione sulla propria", async ({ page, playwright, baseURL }) => {
  const { admin, dev, week } = await prepara(playwright, baseURL!);
  await admin.req.dispose();
  await dev.req.dispose();
  await login(page, "dev");
  await page.goto("/da-fare");
  await expect(page.getByRole("heading", { name: "Da fare" }).first()).toBeVisible();
  await expect(page.locator(`a[href*="settimana=${week}"]`)).toHaveCount(0);
});
