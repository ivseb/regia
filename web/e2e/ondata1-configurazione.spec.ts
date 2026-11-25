// Ondata 1, configurazione e permessi (replica/usabilita.md, sezioni 2 e 4): menu dati del
// costruttore senza chiavi, riepilogo dei permessi nei ruoli, tipo di dato del campo con
// esempio, stati vuoti con pulsante, avviso di migrazione, pannello fisso dell'editor dei
// processi con sezioni comprimibili, un solo posto per gli ambienti di produzione.
import { test, expect } from "@playwright/test";
import { login, uniq, scegli } from "./fixtures";

test("menu dati del costruttore: nomi senza chiavi, le chiavi stanno in «Avanzato»", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/processi/demand_standard");
  await expect(page.getByRole("heading", { name: "Applicabilità" })).toBeVisible();
  const builder = page.getByRole("group", { name: "Si applica quando" });
  await builder.getByRole("button", { name: "+ Aggiungi una riga" }).click();
  const dato = builder.getByLabel("Dato").first();
  await expect(dato).toBeVisible();
  const voci = async () => {
    await dato.click();
    const t = await page.getByRole("option").allTextContents();
    await page.keyboard.press("Escape");
    return t;
  };
  const labels = await voci();
  expect(labels.length).toBeGreaterThan(5);
  // Nessun nome porta la chiave tecnica accanto («Nome · chiave»).
  expect(labels.filter((l) => l.includes(" · "))).toEqual([]);
  expect(labels.some((l) => /^[a-z]+(_[a-z]+)+$/.test(l.trim()))).toBe(false);
  // «Avanzato» mostra le chiavi accanto ai nomi.
  await builder.getByLabel(/^Avanzato/).check();
  const advanced = await voci();
  expect(advanced.filter((l) => l.includes(" · ")).length).toBeGreaterThan(0);
  expect(advanced.length).toBeGreaterThanOrEqual(labels.length);
});

test("ruoli: l'elenco riassume i permessi per nome", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/configurazione/persone?tab=ruoli");
  const riepiloghi = page.getByTestId("riepilogo-permessi");
  await expect(riepiloghi.first()).toBeVisible();
  const testi = await riepiloghi.allTextContents();
  // Nomi in italiano con la maiuscola, mai le chiavi nude dei permessi.
  expect(testi.some((t) => /^[A-ZÈ][a-zè]+ /.test(t.trim()))).toBe(true);
  expect(testi.filter((t) => /\b[a-z]+\.[a-z_]+\b/.test(t))).toEqual([]);
});

test("nuovo campo: nessun tipo predefinito, esempio per tipo e avviso «non si cambia dopo»", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/configurazione/campi");
  await page.getByRole("button", { name: "Nuovo campo" }).click();
  const editor = page.getByRole("complementary", { name: "Modifica del campo" });
  await editor.getByLabel("Nome", { exact: true }).fill(uniq("Campo di prova"));
  const tipo = editor.getByLabel(/^Tipo di dato/);
  await expect(tipo).toHaveAttribute("data-value", "");
  await expect(editor.getByRole("button", { name: "Crea il campo" })).toBeDisabled();
  await expect(editor.getByText(/dopo la creazione il tipo non si potrà più cambiare/)).toBeVisible();
  await scegli(tipo, "Numero intero");
  await expect(editor.getByText(/Un numero senza decimali: 12 utenti/)).toBeVisible();
  await scegli(tipo, "Sì / no");
  await expect(editor.getByText(/Una risposta sì o no/)).toBeVisible();
  await expect(editor.getByRole("button", { name: "Crea il campo" })).toBeEnabled();
});

test("valutazioni: lo stato vuoto spiega e porta al passo successivo", async ({ page }) => {
  await login(page, "sponsor");
  await page.goto("/valutazioni");
  const vuoto = page.getByTestId("stato-vuoto").first();
  await expect(vuoto).toBeVisible();
  await expect(vuoto).toContainText("Nessun questionario da compilare");
  await vuoto.getByRole("link", { name: "Vai alle richieste" }).click();
  await expect(page).toHaveURL(/\/richieste/);
});

test("elenco dei processi: avviso e link alla migrazione delle richieste rimaste su una versione precedente", async ({ page }) => {
  await login(page, "admin");
  // Una versione precedente con 12 richieste: si simula nella risposta, senza toccare i dati condivisi.
  await page.route("**/api/v1/processes", async (route) => {
    const r = await route.fetch();
    const body = await r.json();
    const p = body.items.find((x: { key: string }) => x.key === "demand_standard");
    const cur = p.versions.find((v: { status: string }) => v.status === "published");
    p.versions.push({ ...cur, id: "00000000-0000-0000-0000-000000000001", number: Math.max(0, cur.number - 1) || 0, status: "superseded", elements: 12 });
    await route.fulfill({ response: r, json: body });
  });
  await page.goto("/processi");
  const riga = page.getByRole("row").filter({ hasText: "demand_standard" });
  const avviso = riga.getByRole("link", { name: /12 richieste sono ancora su v\d+/ });
  await expect(avviso).toBeVisible();
  await expect(avviso).toContainText("Migra");
  await avviso.click();
  await expect(page).toHaveURL(/\/processi\/demand_standard#migrazione/);
  await expect(page.getByRole("heading", { name: "Migrazione delle richieste in corso" })).toBeVisible();
});

test("processi: priorità obbligatoria nell'applicabilità", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/processi/demand_standard");
  const priorita = page.getByLabel("Priorità", { exact: true });
  const valore = await priorita.inputValue();
  await priorita.fill("");
  await expect(page.getByRole("alert").filter({ hasText: "La priorità è obbligatoria" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Pubblica", exact: true }).first()).toBeDisabled();
  await priorita.fill(valore);
  await expect(page.getByText("La priorità è obbligatoria")).toHaveCount(0);
});

test("editor dei processi: pannello fisso con sezioni comprimibili, senza barra di scorrimento propria", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/processi");
  await page.getByRole("button", { name: "Nuovo processo" }).click();
  const form = page.getByRole("region", { name: "Nuovo processo" });
  await form.getByLabel("Copiando un processo esistente").check();
  await scegli(form.getByLabel("Processo da copiare"), "Ciclo di vita del bug · Bug");
  await form.getByLabel("Nome", { exact: true }).fill(uniq("Bug pannello"));
  await form.getByLabel("Priorità").fill("902");
  await form.getByRole("button", { name: "Crea e apri l'editor" }).click();
  await expect(page.locator("#editor-diagram")).toBeVisible();

  await page.getByRole("button", { name: "+ Fase" }).click();
  const props = page.getByRole("complementary", { name: "Proprietà dell'elemento selezionato" });
  // Sezioni principali aperte, secondarie chiuse (si aprono con un clic).
  await expect(props.getByLabel(/^Nome/).first()).toBeVisible();
  await expect(props.getByRole("button", { name: "+ Nuovo passaggio da questa fase" })).toBeVisible();
  const aspetto = props.locator("details", { hasText: "Aspetto" });
  await expect(aspetto).not.toHaveAttribute("open", "");
  await expect(props.getByLabel("Altro colore")).toBeHidden();
  await aspetto.locator("summary").click();
  await expect(props.getByLabel("Altro colore")).toBeVisible();
  await aspetto.locator("summary").click();
  await expect(props.getByLabel("Altro colore")).toBeHidden();

  // Il pannello entra nello schermo: nessuna barra di scorrimento propria.
  const scorre = await props.evaluate((el) => el.scrollHeight > el.clientHeight + 1);
  expect(scorre).toBe(false);

  // Il pannello di un passaggio ha le stesse sezioni, con i gate già a vista.
  await props.getByRole("button", { name: "+ Nuovo passaggio da questa fase" }).click();
  await expect(props.getByRole("button", { name: "+ Nuovo gate per questo passaggio" })).toBeVisible();
  const condizione = props.locator("details", { hasText: "Condizione e approvazione" });
  await expect(condizione).not.toHaveAttribute("open", "");
  const scorre2 = await props.evaluate((el) => el.scrollHeight > el.clientHeight + 1);
  expect(scorre2).toBe(false);
});

test("ambienti di produzione: un solo posto, la tabella con i nomi attesi", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/configurazione/devsecops");
  await expect(page.getByRole("heading", { name: "Ambienti di produzione" })).toBeVisible();
  // Il vecchio campo di testo non c'è più.
  await expect(page.getByLabel("Nomi di produzione per gli ambienti nuovi")).toHaveCount(0);
  const sezione = page.getByRole("region", { name: "Ambienti di produzione" });
  const nome = uniq("prod-prova").replaceAll(" ", "-");
  await sezione.getByLabel("Nome di un ambiente di produzione").fill(nome);
  await sezione.getByRole("button", { name: "Aggiungi" }).click();
  await expect(sezione.getByRole("button", { name: `Togli ${nome}` })).toBeVisible();
  await sezione.getByRole("button", { name: `Togli ${nome}` }).click();
  await expect(sezione.getByRole("button", { name: `Togli ${nome}` })).toHaveCount(0);
});

test("processi: la condizione dei gate si legge in parole, non nella sintassi della query", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/processi/demand_standard");
  await page.getByRole("tab", { name: "Definizione" }).click();
  const gate = page.getByRole("heading", { name: "Gate", level: 4 });
  await expect(gate).toBeVisible();
  const tabella = page.locator("table").filter({ has: page.getByRole("columnheader", { name: "Condizione" }) });
  const testo = await tabella.innerText();
  expect(testo).not.toMatch(/\b[a-z]+(_[a-z]+)+: ?[<>a-z]/);
  expect(testo).toMatch(/Stima max/);
});
