// Ondata 3, proposta 8: configurare i processi senza sapere come. «Aggiungi un passo» con condizione e
// gate nello stesso riquadro, diagramma che si adatta e si ingrandisce, avviso a parità di priorità,
// mappa dei processi, accettazione del rischio e scadenze degli incidenti a righe, 375 px.
import { test, expect, type Page } from "@playwright/test";
import { call, expectNoHorizontalScroll, login, scegli, uniq } from "./fixtures";

/** Un processo nuovo copiato dallo standard delle richieste, con priorità alta (valgono prima gli altri). */
async function nuovoProcesso(page: Page, priority: number, name = uniq("Processo guidato")) {
  const key = name.toLowerCase().replaceAll(/[^a-z0-9]+/g, "_");
  await call(page.request, "POST", "/api/v1/processes", { key, name, type: "business_request", priority, copy_from: "demand_standard" });
  return { key, name };
}

test("aggiungi un passo: condizione e gate nello stesso riquadro, fase già selezionata e visibile", async ({ page }) => {
  await login(page, "admin");
  const { key } = await nuovoProcesso(page, 910);
  await page.goto(`/processi/${key}`);
  await page.getByRole("button", { name: "Aggiungi un passo" }).click();
  const form = page.getByRole("form", { name: "Aggiungi un passo al processo" });
  await expect(form).toBeVisible();
  await form.getByLabel("Come si chiama il passo").fill("Collaudo utente");
  await scegli(form.getByLabel("Aggiungi un ruolo…"), { index: 1 });
  await expect(form.getByRole("button", { name: /^Togli il ruolo/ })).toBeVisible();

  // Condizione e gate si compilano qui, nello stesso riquadro.
  await form.getByLabel("Il passo serve solo in certi casi (condizione)").check();
  await form.getByRole("button", { name: "Scrivi come testo" }).first().click();
  await form.getByRole("textbox", { name: /come testo/ }).first().fill("stima_max: >1000");
  await form.getByLabel("Prima di entrare serve un controllo da superare (gate)").check();
  const gate = form.getByTestId("passo-gate");
  await gate.getByLabel("Nome del controllo").fill("Stima presente");
  await gate.getByRole("button", { name: "Scrivi come testo" }).click();
  await gate.getByRole("textbox", { name: /come testo/ }).fill("stima_max: presente");
  await gate.getByLabel("Messaggio quando non è superato").fill("Serve una stima.");
  await form.getByRole("button", { name: "Aggiungi il passo" }).click();

  // La fase nuova è selezionata e il suo pannello è aperto; si vede nel diagramma.
  await expect(form).toHaveCount(0);
  const props = page.getByRole("complementary", { name: "Proprietà dell'elemento selezionato" });
  await expect(props.getByLabel(/^Nome/).first()).toHaveValue("Collaudo utente");
  const nodo = page.getByRole("group", { name: "Diagramma delle fasi e dei passaggi" }).getByRole("button", { name: /^Collaudo utente/ });
  await expect(nodo).toBeVisible();
  const box = page.locator("#editor-diagram .overflow-x-auto").first();
  const dentro = await nodo.evaluate((el, b) => {
    const r = el.getBoundingClientRect();
    const c = (b as Element).getBoundingClientRect();
    return r.left >= c.left - 1 && r.right <= c.right + 1;
  }, await box.elementHandle());
  expect(dentro, "la fase nuova sta dentro al riquadro del diagramma").toBe(true);

  // La definizione salvata ha fase, passaggio con ruolo e condizione, e il gate.
  await page.getByRole("button", { name: "Salva bozza" }).click();
  const salvata = async () => {
    const p = await call(page.request, "GET", `/api/v1/processes/${key}`);
    return p.draft ?? p.current;
  };
  await expect.poll(async () => (await salvata()).phases.map((x: { name: string }) => x.name)).toContain("Collaudo utente");
  const d = await salvata();
  expect(d.phases.map((x: { name: string }) => x.name)).toContain("Collaudo utente");
  const entra = d.transitions.find((t: { to: string }) => t.to === "Collaudo utente");
  expect(entra.when).toBe("stima_max: >1000");
  expect(entra.roles?.length).toBe(1);
  expect(entra.gates?.length).toBe(1);
  expect(d.gates.find((g: { name: string }) => g.name === "Stima presente").condition).toBe("stima_max: presente");
});

test("diagramma: adatta, zoom e centra sulla fase selezionata", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/processi/demand_standard");
  const diagram = page.getByRole("group", { name: "Diagramma delle fasi e dei passaggi" });
  await expect(diagram).toBeVisible();
  const box = page.locator("#editor-diagram .overflow-x-auto").first();
  const percent = page.getByTestId("zoom-percent");
  const valore = async () => Number((await percent.innerText()).replace(/\D/g, ""));
  await page.getByRole("button", { name: "Adatta alla larghezza" }).click();
  // Adattato: sta nel riquadro senza scorrere.
  expect(await box.evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
  const adattato = await valore();
  await page.getByRole("button", { name: "Ingrandisci il diagramma" }).click();
  expect(await valore()).toBeGreaterThan(adattato);
  await page.getByRole("button", { name: "Dimensioni reali" }).click();
  expect(await valore()).toBe(100);
  await page.getByRole("button", { name: "Riduci il diagramma" }).click();
  expect(await valore()).toBeLessThan(100);
  await page.getByRole("button", { name: "Adatta alla larghezza" }).click();
  expect(await valore()).toBe(adattato);
  // Ingrandito, si centra su una fase selezionata (l'ultima).
  await page.getByRole("button", { name: "Dimensioni reali" }).click();
  const def = (await call(page.request, "GET", "/api/v1/processes/demand_standard")).current;
  const ultima = def.phases.filter((x: { off_path?: boolean }) => !x.off_path).at(-1).name as string;
  await diagram.getByRole("button", { name: new RegExp(`^${ultima}`) }).first().click();
  await page.getByRole("button", { name: "Centra sulla fase" }).click();
  await expect.poll(async () => box.evaluate((el) => el.scrollLeft)).toBeGreaterThan(0);
});

test("a parità di priorità l'avviso chiede quale deve vincere", async ({ page }) => {
  await login(page, "admin");
  const a = await nuovoProcesso(page, 920, uniq("Pari A"));
  await nuovoProcesso(page, 920, uniq("Pari B"));
  await page.goto(`/processi/${a.key}`);
  const avviso = page.getByTestId("parita-priorita");
  await expect(avviso).toContainText("quale deve vincere");
  await expect(avviso).toContainText("Pari B");
  await page.getByLabel("Priorità", { exact: true }).fill("921");
  await expect(avviso).toHaveCount(0);
  // Senza priorità la bozza non si pubblica.
  await page.getByLabel("Priorità", { exact: true }).fill("");
  await expect(page.getByRole("alert").filter({ hasText: "priorità è obbligatoria" })).toBeVisible();
});

test("mappa dei processi: miniatura, versione e stato di migrazione", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/processi");
  await page.getByRole("button", { name: "Mappa", exact: true }).click();
  const mappa = page.getByTestId("mappa-processi");
  await expect(mappa).toBeVisible();
  const scheda = mappa.getByRole("listitem").filter({ hasText: "Ciclo di vita della domanda" }).first();
  await expect(scheda.getByRole("img", { name: /^Fasi del processo/ })).toBeVisible();
  await expect(scheda).toContainText(/v\d+/);
  await expect(scheda).toContainText(/Tutto sulla v\d+|ancora su|ancora sulla/);
  // Nessun codice nella condizione: o «Sempre» o «Solo se …».
  await expect(mappa).not.toContainText(/\w+: \w+/);
});

test("accettazione del rischio e scadenze degli incidenti a righe e menu", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/rischi?tab=configurazione");
  const regole = page.getByTestId("regola-accettazione");
  await expect(regole.first()).toBeVisible();
  // Passi e approvatori sono menu, il testo grezzo sta in «Avanzato» chiuso.
  await expect(regole.first().getByTestId("passi-accettazione").getByRole("combobox").first()).toBeVisible();
  const avanzato = regole.first().locator("details");
  await expect(avanzato).not.toHaveAttribute("open", "");
  await expect(avanzato.getByLabel("Passi")).toBeHidden();
  await avanzato.locator("summary").click();
  await expect(avanzato.getByLabel("Passi")).toBeVisible();

  // Scadenze: una linea del tempo e, in modifica, righe con quantità, unità e punto di partenza.
  const linea = page.getByTestId("linea-del-tempo").first();
  await expect(linea).toBeVisible();
  await expect(linea).toContainText(/\d+ or[ae]|\d+ giorn/);
  await page.getByRole("button", { name: "Modifica" }).last().click();
  const tappa = page.getByTestId("tappa").first();
  await expect(tappa.getByTestId("termine").first().getByRole("spinbutton")).toBeVisible();
  await expect(tappa.getByText("dal rilevamento").first()).toBeVisible();
});

test.describe("telefono", () => {
  test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

  test("375 px: elenco dei processi come mappa, editor come elenco di passi, rischi senza scorrimento", async ({ page }) => {
    await login(page, "admin");
    await page.goto("/processi");
    await expect(page.getByTestId("mappa-processi")).toBeVisible();
    await expectNoHorizontalScroll(page);
    await page.goto("/processi/demand_standard");
    await expect(page.getByRole("heading", { level: 1, name: /./ })).toBeVisible();
    await expect(page.getByRole("group", { name: "Diagramma delle fasi e dei passaggi" })).toHaveCount(0);
    await page.getByRole("button", { name: "Aggiungi un passo" }).click();
    const form = page.getByRole("form", { name: "Aggiungi un passo al processo" });
    await expect(form).toBeVisible();
    for (const b of await form.getByRole("button").all()) {
      const r = await b.boundingBox();
      if (r) expect(r.height, "bersaglio di almeno 44 px").toBeGreaterThanOrEqual(43);
    }
    await expectNoHorizontalScroll(page);
    await page.goto("/rischi?tab=configurazione");
    await expect(page.getByTestId("regola-accettazione").first()).toBeVisible();
    await expectNoHorizontalScroll(page);
  });
});
