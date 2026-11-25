// Ondata «semplicità»: percorso guidato dell'amministratore e mappa d'insieme dei processi. Il percorso mostra
// le tappe con lo stato calcolato dai dati veri e porta alle pagine giuste; la mappa mostra fasi, team,
// valutazioni e approvazioni dei processi pubblicati, in sola lettura per chi non configura; da telefono è
// un elenco verticale. Il tenant «avvio» è condiviso con ondata3-avvio: qui si legge soltanto.
import { test, expect } from "@playwright/test";
import { call, expectNoHorizontalScroll, login } from "./fixtures";

type Mappa = { processes: { key: string; type_name: string; phases: { name: string; items: { kind: string }[] }[] }[] };
type Percorso = { tappe: { key: string; status: string; issues: { code: string; count: number }[] }[]; done: number; total: number };

test("percorso guidato: sei tappe con stato calcolato, ognuna porta alla pagina giusta", async ({ page }) => {
  await login(page, "avvio", "avvio");
  await page.goto("/configurazione/percorso");
  await expect(page.getByRole("heading", { name: "Percorso guidato", level: 1 })).toBeVisible();
  const api = await call<Percorso>(page.request, "GET", "/api/v1/avvio/percorso");
  expect(api.tappe.map((t) => t.key)).toEqual(["persone", "team", "processi", "valutazioni", "spazi", "moduli"]);
  for (const t of api.tappe) {
    const riga = page.getByTestId(`percorso-tappa-${t.key}`);
    await expect(riga).toBeVisible();
    await expect(riga).toHaveAttribute("data-status", /done|partial|todo|off/);
    await expect(page.getByTestId(`percorso-tappa-${t.key}-conteggi`)).not.toBeEmpty();
  }
  await expect(page.getByTestId("percorso-progress")).toContainText(`${api.done} di ${api.total} tappe fatte`);
  await expect(page.getByTestId("percorso-sentiero")).toBeVisible();

  // «Usa un modello» porta ai modelli di «Inizia da qui»; la mappa si apre dal percorso.
  await page.getByTestId("percorso-tappa-processi").getByRole("link", { name: "Usa un modello" }).click();
  await expect(page).toHaveURL(/\/configurazione\/avvio\?tab=modelli/);
  await page.goBack();
  await page.getByTestId("percorso-tappa-processi").getByRole("link", { name: "Vedi la mappa dei processi" }).click();
  await expect(page).toHaveURL(/\/mappa-processi/);

  await page.goto("/configurazione/percorso");
  await page.getByTestId("percorso-tappa-team").getByRole("link", { name: /^(Vai|Rivedi)/ }).first().click();
  await expect(page).toHaveURL(/\/architettura/);
});

test("percorso su organizzazione attiva: segnala le incoerenze con il collegamento per sistemarle", async ({ page }) => {
  await login(page, "admin");
  const api = await call<Percorso>(page.request, "GET", "/api/v1/avvio/percorso");
  expect(api.total).toBeGreaterThan(3);
  await page.goto("/configurazione/percorso");
  for (const t of api.tappe) {
    for (const is of t.issues) await expect(page.getByTestId(`percorso-incoerenza-${is.code}`)).toBeVisible();
  }
  // Riapribile anche dopo aver chiuso «Inizia da qui»: il percorso resta raggiungibile dalla configurazione.
  await page.goto("/configurazione");
  await expect(page.getByTestId("ingresso-percorso")).toBeVisible();
  await expect(page.getByTestId("ingresso-mappa")).toBeVisible();
  await page.getByTestId("ingresso-percorso").click();
  await expect(page).toHaveURL(/\/configurazione\/percorso/);
});

test("mappa dei processi: fasi in fila con valutazioni e approvazioni agganciate; l'amministratore apre la configurazione", async ({ page }) => {
  await login(page, "admin");
  const mappa = await call<Mappa>(page.request, "GET", "/api/v1/mappa-processi");
  expect(mappa.processes.length).toBeGreaterThan(0);
  await page.goto("/mappa-processi");
  await expect(page.getByRole("heading", { name: "Mappa dei processi", level: 1 })).toBeVisible();
  for (const p of mappa.processes) {
    const sez = page.getByTestId(`mappa-processo-${p.key}`);
    await expect(sez).toBeVisible();
    await expect(sez.locator("[data-testid^='mappa-fase-']")).toHaveCount(p.phases.length);
  }
  const conItems = mappa.processes.find((p) => p.phases.some((f) => f.items.length > 0));
  expect(conItems, "almeno un processo con elementi agganciati").toBeTruthy();
  // Clic su un elemento: si apre la pagina di configurazione.
  const voce = page.getByTestId(`mappa-processo-${conItems!.key}`).locator("[data-testid^='mappa-voce-']").first();
  await voce.click();
  await expect(page).toHaveURL(/\/processi\/|\/configurazione\/valutazioni/);
});

test("mappa dei processi: chi non configura la legge senza collegamenti di modifica", async ({ page }) => {
  await login(page, "dm");
  await page.goto("/mappa-processi");
  await expect(page.getByRole("heading", { name: "Mappa dei processi", level: 1 })).toBeVisible();
  await expect(page.locator("[data-testid^='mappa-processo-']").first()).toBeVisible();
  await expect(page.locator("[data-testid^='mappa-voce-'][href]")).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Configura i processi" })).toHaveCount(0);
});

test("telefono a 375 px: percorso e mappa senza scorrimento orizzontale, fasi in elenco verticale", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "admin");
  await page.goto("/configurazione/percorso");
  await expect(page.getByTestId("percorso-tappa-team")).toBeVisible();
  await expectNoHorizontalScroll(page);
  await expect(page.getByTestId("percorso-sentiero")).toBeHidden();
  await page.goto("/mappa-processi");
  const fasi = page.locator("[data-testid^='mappa-fasi-']").first();
  await expect(fasi).toBeVisible();
  await expectNoHorizontalScroll(page);
  const tops = await fasi.locator("> li").evaluateAll((els) => els.slice(0, 2).map((e) => e.getBoundingClientRect().top));
  expect(tops[1], "le fasi sono una sotto l'altra").toBeGreaterThan(tops[0]);
});
