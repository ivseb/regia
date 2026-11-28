// Automazioni (docs/sviluppo/automazioni.md): nell'editor «Quando… allora…» le regole a tempo,
// periodiche e su cambio di campo si compongono come frasi, con le azioni nuove, l'anteprima dei
// prossimi 30 giorni e il formato salvato di sempre. Gli effetti (lavoratore, idempotenza, cicli)
// li prova il test di integrazione internal/itest/automazioni_test.go.
import { test, expect, type Page } from "@playwright/test";
import { call, login, scegli, uniq } from "./fixtures";

async function nuovoProcesso(page: Page) {
  const name = uniq("Processo automazioni");
  const key = name.toLowerCase().replaceAll(/[^a-z0-9]+/g, "_");
  // Priorità bassa: il processo non prende le richieste degli altri test.
  await call(page.request, "POST", "/api/v1/processes", { key, name, type: "business_request", priority: 950, copy_from: "demand_standard" });
  return key;
}

type Regola = { on: string; when?: string; actions: { kind: string; params?: Record<string, string> }[] };

async function regole(page: Page, key: string): Promise<Regola[]> {
  const p = await call(page.request, "GET", `/api/v1/processes/${key}`);
  return ((p.draft ?? p.current).rules ?? []) as Regola[];
}

test("regola a tempo: 30 giorni prima della scadenza aggiungi un commento, con l'anteprima", async ({ page }) => {
  await login(page, "admin");
  const key = await nuovoProcesso(page);
  await page.goto(`/processi/${key}`);
  await page.getByRole("button", { name: "+ Nuova regola" }).click();
  const frase = page.getByTestId("regola-frase");
  await expect(frase).toBeVisible();

  await scegli(frase.getByLabel("Evento della regola"), "a una data, qualche giorno prima o dopo…");
  await frase.getByLabel("Quanti giorni").fill("30");
  await scegli(frase.getByLabel("Prima o dopo"), "giorni prima di");
  await scegli(frase.getByLabel("Data dell'elemento"), "Scadenza");
  await expect(frase.getByTestId("regola-anteprima")).toContainText("Nei prossimi 30 giorni questa regola scatterebbe su");

  await scegli(frase.getByLabel("Aggiungi un'azione…"), "Aggiungi un commento");
  await frase.getByLabel("Testo del commento").fill("Scade presto: ");
  await scegli(frase.getByLabel("Inserisci un dato"), { value: "titolo" });

  const elenco = page.getByTestId("regola-frase-elenco").last();
  await expect(elenco).toContainText("Quando mancano 30 giorni a «Scadenza»");
  await expect(elenco).toContainText("aggiungi il commento «Scade presto: {{titolo}}»");
  await expect(elenco).not.toContainText("add_comment");

  await page.getByRole("button", { name: "Salva bozza" }).click();
  await expect.poll(async () => (await regole(page, key)).at(-1)?.on, { timeout: 15000 }).toBe("at:scadenza:-30");
  const mia = (await regole(page, key)).at(-1)!;
  expect(mia.actions).toEqual([{ kind: "add_comment", params: { text: "Scade presto: {{titolo}}" } }]);
  // La versione con la regola a tempo si pubblica: il motore la accetta.
  await call(page.request, "POST", `/api/v1/processes/${key}/publish`, { notes: "regola a tempo" });
});

test("su cambio del titolo avvisa chi segue; ogni lunedì, solo se…, la frase si legge", async ({ page }) => {
  await login(page, "admin");
  const key = await nuovoProcesso(page);
  await page.goto(`/processi/${key}`);

  // Quando cambia il titolo → notifica a chi segue l'elemento, con il valore di prima nel testo.
  await page.getByRole("button", { name: "+ Nuova regola" }).click();
  let frase = page.getByTestId("regola-frase");
  await scegli(frase.getByLabel("Evento della regola"), "cambia un campo…");
  await scegli(frase.getByLabel("Campo che cambia"), "Titolo");
  await scegli(frase.getByLabel("Aggiungi un'azione…"), "Manda una notifica");
  await frase.getByLabel("Testo della notifica").fill("Prima era: ");
  await scegli(frase.getByLabel("Inserisci un dato"), { value: "prima.titolo" });
  await frase.getByLabel("Chi segue l'elemento").check();
  await expect(page.getByTestId("regola-frase-elenco").last()).toContainText("Quando cambia il campo «Titolo»");

  // Ogni lunedì → commento.
  await page.getByRole("button", { name: "+ Nuova regola" }).click();
  frase = page.getByTestId("regola-frase");
  await scegli(frase.getByLabel("Evento della regola"), "ogni settimana…");
  await scegli(frase.getByLabel("Giorno della settimana"), "lunedì");
  await expect(frase.getByTestId("regola-anteprima")).toContainText("Al prossimo giro");
  await scegli(frase.getByLabel("Aggiungi un'azione…"), "Aggiungi un commento");
  await frase.getByLabel("Testo del commento").fill("Giro settimanale");
  await expect(page.getByTestId("regola-frase-elenco").last()).toContainText("Ogni lunedì");

  await page.getByRole("button", { name: "Salva bozza" }).click();
  await expect.poll(async () => (await regole(page, key)).at(-1)?.on, { timeout: 15000 }).toBe("every:week:1");
  const tutte = await regole(page, key);
  const suCambio = tutte.at(-2)!;
  expect(suCambio.on).toBe("change:titolo");
  expect(suCambio.actions[0]).toMatchObject({ kind: "notify", params: { message: "Prima era: {{prima.titolo}}", followers: "true" } });
  await call(page.request, "POST", `/api/v1/processes/${key}/publish`, { notes: "automazioni" });
});
