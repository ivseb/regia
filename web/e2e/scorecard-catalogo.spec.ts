// Scorecard e catalogo dei requisiti senza JSON: editor visuali (pesi con cursore, regole col
// costruttore, voci e baseline, importazione CSV), JSON solo nel riquadro «Avanzato».
// La scorecard si prova nel tenant «altro» (non condiviso con gli altri test sulla graduatoria).
import { test, expect } from "@playwright/test";
import { call, expectNoHorizontalScroll, login, scegli } from "./fixtures";

type Def = { criteria: { key: string; weight: number; suggest?: { when: string; value: number }[] }[] };

test("scorecard: peso e regola di proposta dal modulo, poi pubblica", async ({ page }) => {
  await login(page, "admin", "altro");
  await page.goto("/graduatoria?vista=scorecard");
  const editor = page.getByRole("region", { name: "Editor della scorecard" });
  await expect(editor).toBeVisible();
  // Nessuna casella di JSON in vista: sta nel riquadro «Avanzato», chiuso.
  await expect(page.getByTestId("avanzato-json")).not.toHaveAttribute("open", "");
  await expect(page.getByRole("textbox", { name: /Definizione della scorecard/ })).toBeHidden();

  const crit = editor.getByTestId("criterio").first();
  const peso = crit.getByRole("spinbutton", { name: /^Peso di .* \(numero\)$/ });
  const prima = Number(await peso.inputValue());
  const totale = editor.getByTestId("pesi-totale");
  const t0 = Number((await totale.innerText()).match(/Peso totale: ([\d.]+)/)![1]);
  await peso.fill(String(prima + 7));
  await expect(totale).toContainText(`Peso totale: ${Math.round((t0 + 7) * 100) / 100} su 100`);
  await expect(crit.getByTestId("peso-percento")).toContainText("% del totale");
  await expect(totale).toContainText("non fanno 100");
  // Il cursore e il numero sono lo stesso valore.
  await expect(crit.getByRole("slider", { name: /^Peso di .* \(cursore\)$/ })).toHaveValue(String(prima + 7));

  // Regola di proposta con il costruttore («Solo se…» e conteggio dal vivo).
  await crit.getByRole("button", { name: "+ Aggiungi una regola di proposta" }).click();
  const regola = crit.getByRole("group", { name: /Regola \d+: proponi quando/ }).last();
  await regola.getByRole("button", { name: "+ Aggiungi una riga" }).click();
  await scegli(regola.getByLabel("Dato").first(), { index: 0 });
  await scegli(regola.getByLabel("Operatore"), /presente/);
  await expect(regola.getByTestId("cond-frase")).toContainText("Solo se");
  await expect(regola.getByTestId("cond-conteggio")).toBeVisible();

  await editor.getByRole("button", { name: "Salva la bozza" }).click();
  const pubblica = editor.getByRole("button", { name: /^Pubblica la v/ });
  await expect(pubblica).toBeEnabled();
  await pubblica.click();
  await expect(editor.getByRole("button", { name: /^Pubblica la v/ })).toBeDisabled();
  const lista = await call<{ items: { key: string }[] }>(page.request, "GET", "/api/v1/scorecards");
  const key = lista.items[0].key;
  const sc = await call<{ current: { definition: Def }; draft: unknown }>(page.request, "GET", `/api/v1/scorecards/${key}`);
  expect(sc.draft).toBeNull();
  const c0 = sc.current.definition.criteria[0];
  expect(c0.weight).toBe(prima + 7);
  expect(c0.suggest?.length ?? 0).toBeGreaterThan(0);
  expect(c0.suggest![c0.suggest!.length - 1].when).not.toBe("");
});

test("scorecard: i problemi del server stanno accanto al criterio", async ({ page }) => {
  await login(page, "admin", "altro");
  await page.goto("/graduatoria?vista=scorecard");
  const editor = page.getByRole("region", { name: "Editor della scorecard" });
  const crit = editor.getByTestId("criterio").first();
  await crit.getByRole("spinbutton", { name: /\(numero\)$/ }).fill("0");
  await editor.getByRole("button", { name: "Salva la bozza" }).click();
  await expect(crit.getByTestId("riga-problemi")).toContainText("peso");
});

test("catalogo: voce, baseline, importazione CSV e pubblicazione senza JSON", async ({ page }) => {
  const key = `prova_${Date.now().toString(36)}`;
  await login(page, "admin");
  await page.goto(`/configurazione/requisiti?tab=cataloghi&catalogo=${key}`);
  const editor = page.getByRole("region", { name: /^(Bozza|Nuova versione)/ });
  await expect(editor).toBeVisible();
  await expect(page.getByTestId("avanzato-json")).not.toHaveAttribute("open", "");

  await editor.getByLabel("Nome", { exact: true }).first().fill("Catalogo di prova");
  // Una voce nuova.
  await editor.getByRole("button", { name: "+ Aggiungi una voce" }).click();
  const voci = editor.getByTestId("voce");
  await expect(voci).toHaveCount(2);
  const nuova = voci.nth(1);
  await nuova.getByLabel("Titolo").fill("Cifratura dei dati a riposo");
  await nuova.getByLabel("Categoria").fill("Dati");
  await voci.nth(0).getByLabel("Titolo").fill("Accessi con autenticazione forte");

  // Importazione CSV: anteprima, poi applica.
  await editor.getByRole("button", { name: "Importa CSV" }).click();
  const csv = page.getByTestId("csv-import");
  await csv.getByLabel("Oppure incolla il contenuto").fill("key,title,text,category,method\nREQ-10,Backup verificati,Prova di ripristino ogni trimestre,Continuità,manual\n,Senza chiave,,,manual");
  await expect(csv.getByRole("status")).toContainText("1 righe valide su 2");
  await expect(csv.getByTestId("csv-anteprima")).toContainText("manca la chiave");
  await csv.getByRole("button", { name: "Applica 1 voce alla bozza" }).click();
  await expect(voci).toHaveCount(3);

  // Una baseline con le voci scelte da menu.
  await editor.getByRole("button", { name: "+ Aggiungi una baseline" }).click();
  const base = editor.getByTestId("baseline").last();
  await base.getByLabel("Nome", { exact: true }).fill("Base di prova");
  await base.getByRole("button", { name: "+ Aggiungi una riga" }).click();
  await expect(base.getByTestId("cond-conteggio")).toBeVisible();
  // Scelta multipla: il menu resta aperto dopo ogni voce.
  await base.getByLabel(/^Voci della baseline/).click();
  await page.getByRole("option", { name: /REQ-10/ }).click();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("listbox")).toHaveCount(0);

  await editor.getByRole("button", { name: "Salva la bozza" }).click();
  await expect(editor.getByRole("button", { name: "Pubblica", exact: true })).toBeEnabled();
  await editor.getByRole("button", { name: "Pubblica", exact: true }).click();
  await expect.poll(async () => (await call<{ current: { definition: { items: { key: string }[]; baselines: { name: string; items: string[] }[] } } | null }>(page.request, "GET", `/api/v1/requirement-config/catalogs/${key}`)).current?.definition.items.length).toBe(3);
  const cat = await call<{ current: { definition: { items: { key: string }[]; baselines: { name: string; items: string[] }[] } } }>(page.request, "GET", `/api/v1/requirement-config/catalogs/${key}`);
  expect(cat.current.definition.items.map((i) => i.key)).toContain("REQ-10");
  expect(cat.current.definition.baselines.some((b) => b.name === "Base di prova" && b.items.includes("REQ-10"))).toBeTruthy();
});

test.describe("telefono", () => {
  test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });
  test("375 px senza scorrimento orizzontale: scorecard e catalogo", async ({ page }) => {
    await login(page, "admin", "altro");
    await page.goto("/graduatoria?vista=scorecard");
    await expect(page.getByRole("region", { name: "Editor della scorecard" })).toBeVisible();
    await page.waitForLoadState("networkidle");
    await expectNoHorizontalScroll(page);
    await login(page, "admin");
    await page.goto(`/configurazione/requisiti?tab=cataloghi&catalogo=telefono_${Date.now().toString(36)}`);
    await expect(page.getByTestId("voce").first()).toBeVisible();
    await page.waitForLoadState("networkidle");
    await expectNoHorizontalScroll(page);
  });
});
