// Ondata 2, configurazione (replica/usabilita.md, proposte 6 e 7A): costruttore unico di
// condizioni (righe, gruppi, frase, conteggio), indice «Configurazione» con ricerca,
// «modifica → anteprima → pubblica», «Quando si apre» e righe per esiti e ruoli nel tipo di
// valutazione, import unificati con i modelli CSV. Una parte in inglese.
import { test, expect } from "@playwright/test";
import { call, login, uniq, scegli } from "./fixtures";

test("condizione costruita visivamente: righe, gruppi, frase e conteggio degli elementi", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/processi/demand_standard");
  const builder = page.getByRole("group", { name: "Si applica quando" });
  await expect(builder).toBeVisible();
  await builder.getByRole("button", { name: "+ Aggiungi una riga" }).click();
  await scegli(builder.getByLabel("Dato").first(), /^Driver/);
  const valore = builder.getByLabel("Aggiungi un valore").first();
  await valore.fill("normativo");
  await valore.press("Enter");

  // La frase in parole e il conteggio sono sempre visibili e seguono la condizione.
  await expect(builder.getByTestId("cond-frase")).toContainText(/^Solo se .*normativo/);
  await expect(builder.getByTestId("cond-conteggio")).toContainText(/Oggi la condizione è vera per \d+ su \d+/);

  // Un secondo gruppo alternativo («oppure») con la sua riga.
  await builder.getByRole("button", { name: "+ Aggiungi un gruppo alternativo (oppure)" }).click();
  const gruppo2 = builder.getByRole("group", { name: "Gruppo 2" });
  await expect(gruppo2).toBeVisible();
  await gruppo2.getByRole("button", { name: "+ Aggiungi una riga" }).click();
  await scegli(gruppo2.getByLabel("Dato").first(), /^Driver/);
  await gruppo2.getByLabel("Aggiungi un valore").first().fill("strategico");
  await gruppo2.getByLabel("Aggiungi un valore").first().press("Enter");
  await expect(builder.getByTestId("cond-frase")).toContainText(/normativo.* oppure .*strategico/);

  // La sintassi testuale resta in «avanzato»: qui è conforme al linguaggio dei processi.
  await builder.getByRole("button", { name: "Scrivi come testo" }).click();
  await expect(builder.getByRole("textbox", { name: /come testo/ })).toHaveValue(/driver: normativo oppure driver: strategico/);
  await builder.getByRole("button", { name: "Usa il costruttore" }).click();
  await expect(builder.getByRole("group", { name: "Gruppo 2" })).toBeVisible();

  // Niente è salvato: la bozza mostra «Cosa cambia» e si scarta.
  const bozza = page.getByTestId("bozza").first();
  await expect(bozza).toContainText("1 modifica non salvata");
  await expect(bozza.getByTestId("bozza-differenze")).toContainText("Si applica quando");
  await bozza.getByRole("button", { name: "Scarta le modifiche" }).click();
  await expect(bozza).toContainText("Nessuna modifica");
});

test("stessa condizione visuale in applicabilità GRC, filtro dei webhook e «Visibile se» dei campi", async ({ page }) => {
  await login(page, "admin");
  // Applicabilità delle norme: il conteggio dei perimetri sostituisce il pulsante «Prova».
  await page.goto("/conformita?tab=regole");
  const regola = page.getByRole("group", { name: "Si applica solo se" }).first();
  await expect(regola).toBeVisible();
  await expect(regola.getByTestId("cond-conteggio")).toContainText(/Oggi la condizione è vera per \d+ su \d+/);
  await expect(page.getByRole("button", { name: "Prova", exact: true })).toHaveCount(0);

  // Webhook: il filtro è un costruttore, con i dati di base o del tipo scelto.
  await page.goto("/configurazione/webhook?sel=nuovo");
  const filtro = page.getByRole("group", { name: /Filtro sull'elemento/ });
  await expect(filtro).toBeVisible();
  await filtro.getByRole("button", { name: "+ Aggiungi una riga" }).click();
  await expect(filtro.getByLabel("Dato").first()).toBeVisible();

  // Campi personalizzati: «Visibile se» con lo stesso costruttore.
  const campi = await call<{ items: { key: string; name: string; archived: boolean; types: { type: string }[] }[] }>(page.request, "GET", "/api/v1/fields");
  const campo = campi.items.find((c) => !c.archived && c.types.length > 0)!;
  await page.goto(`/configurazione/campi?campo=${campo.key}`);
  await page.getByRole("button", { name: "Regole", exact: true }).first().click();
  await expect(page.getByRole("group", { name: "Visibile se" })).toBeVisible();
});

test("indice Configurazione: per obiettivo, con ricerca e pagine sparse", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/configurazione");
  await expect(page.getByRole("heading", { name: "Configurazione", level: 1 })).toBeVisible();
  for (const g of ["people", "organization", "types", "processes", "governance", "finance", "devsecops", "compliance", "integrations", "language"]) {
    await expect(page.getByTestId(`obiettivo-${g}`)).toBeVisible();
  }
  // Raccoglie anche le impostazioni che stanno nelle pagine operative.
  await expect(page.getByRole("link", { name: "Apri Applicabilità delle norme" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Apri Scorecard e graduatoria" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Apri Tariffe e ore" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Apri Soglie DORA" })).toBeVisible();

  // Ricerca per nome o per obiettivo, senza maiuscole e accenti.
  await page.getByLabel("Cerca una impostazione").fill("webhook");
  await expect(page.getByTestId("obiettivo-integrations")).toBeVisible();
  await expect(page.getByTestId("obiettivo-people")).toHaveCount(0);
  await page.getByLabel("Cerca una impostazione").fill("conformita norme");
  await expect(page.getByRole("link", { name: "Apri Applicabilità delle norme" })).toBeVisible();
  await page.getByLabel("Cerca una impostazione").fill("xyzxyz");
  await expect(page.getByText("Nessuna impostazione corrisponde alla ricerca.")).toBeVisible();

  await page.getByLabel("Cerca una impostazione").fill("webhook");
  await page.getByRole("link", { name: "Apri Webhook" }).click();
  await expect(page).toHaveURL(/\/configurazione\/webhook/);

  // La voce di menu porta qui.
  await page.getByRole("link", { name: "Configurazione", exact: true }).first().click();
  await expect(page).toHaveURL(/\/configurazione$/);

  // Chi non amministra vede solo ciò che può usare (nessuna pagina di amministrazione).
  await login(page, "dev");
  await page.goto("/configurazione");
  await expect(page.getByRole("link", { name: "Apri Persone, ruoli e gruppi" })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Apri Aspetto" })).toBeVisible();
});

test("processo: bozza → differenze → pubblica (e scarta)", async ({ page }) => {
  await login(page, "admin");
  const lista = await call<{ items: { key: string; name: string; type: string }[] }>(page.request, "GET", "/api/v1/processes");
  const base = lista.items.find((p) => p.name === "Ciclo di vita del bug")!;
  const name = uniq("Bug con bozza");
  const key = name.toLowerCase().replace(/[^a-z0-9]+/g, "_");
  await call(page.request, "POST", "/api/v1/processes", { key, name, type: base.type, copy_from: base.key, priority: 900 });

  await page.goto(`/processi/${key}`);
  const bozza = page.getByRole("region", { name: "Bozza e pubblicazione" });
  await expect(bozza).toContainText("Nessuna modifica");
  const priorita = page.getByLabel("Priorità", { exact: true });
  await priorita.fill("905");
  await expect(bozza).toContainText("1 modifica non salvata");
  await expect(bozza.getByTestId("bozza-differenze")).toContainText(/Priorità\s*900\s*→\s*905/);
  await expect(bozza.getByTestId("bozza-impatto")).toContainText("Impatto previsto");

  // Scarta: torna alla versione pubblicata.
  await bozza.getByRole("button", { name: "Scarta le modifiche" }).click();
  await expect(priorita).toHaveValue("900");
  await expect(bozza).toContainText("Nessuna modifica");

  // Pubblica: la priorità cambia davvero.
  await priorita.fill("907");
  await expect(bozza.getByTestId("bozza-differenze")).toContainText(/900\s*→\s*907/);
  await bozza.getByRole("button", { name: "Pubblica", exact: true }).click();
  await expect(bozza.getByRole("status")).toContainText("Pubblicato.");
  const p = await call<{ priority: number }>(page.request, "GET", `/api/v1/processes/${key}`);
  expect(p.priority).toBe(907);
});

test("tipo di elemento: bozza con «Cosa cambia» prima di pubblicare", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/configurazione/tipi");
  await page.getByRole("region", { name: "Architettura" }).getByRole("button", { name: /capability/ }).click();
  const scheda = page.getByRole("complementary", { name: "Scheda del tipo" });
  const nome = scheda.getByLabel("Nome", { exact: true });
  const prima = await nome.inputValue();
  await nome.fill(`${prima} X`);
  const bozza = scheda.getByRole("region", { name: "Bozza e pubblicazione" });
  await expect(bozza).toContainText("1 modifica non salvata");
  await expect(bozza.getByTestId("bozza-differenze")).toContainText(`${prima} X`);
  await bozza.getByRole("button", { name: "Scarta le modifiche" }).click();
  await expect(nome).toHaveValue(prima);
});

test("gate di rilascio: bozza dell'ambiente con «Cosa cambia»", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/configurazione/gate");
  const bozza = page.getByRole("region", { name: "Bozza e pubblicazione" });
  await expect(bozza).toBeVisible();
  await expect(bozza).toContainText("Nessuna modifica");
  await page.getByRole("radio", { name: /^Spento|^Blocca|^Avvisa/ }).last().check();
  await expect(bozza).toContainText(/modific[ah]e? non salvat[ae]/);
  await bozza.getByRole("button", { name: "Scarta le modifiche" }).click();
  await expect(bozza).toContainText("Nessuna modifica");
});

test("tipo di valutazione: «Quando si apre» con le regole dei processi, esiti e ruoli come righe", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/configurazione/valutazioni?tab=tipi");
  const quando = page.getByTestId("quando-si-apre").first();
  await expect(quando).toBeVisible();
  // Almeno un tipo ha una regola di apertura nel processo della domanda.
  await expect(page.getByTestId("quando-si-apre").filter({ hasText: /entra nella fase/i }).first()).toBeVisible();

  await page.getByRole("button", { name: "Modifica" }).first().click();
  const esiti = page.getByTestId("esiti");
  await expect(esiti.getByRole("textbox").first()).toBeVisible();
  const n = await esiti.getByRole("listitem").count();
  await page.getByRole("button", { name: "+ Aggiungi un esito" }).click();
  await expect(esiti.getByRole("listitem")).toHaveCount(n + 1);
  await esiti.getByRole("textbox").last().fill("Rinviato");
  await scegli(page.getByLabel(`Effetto dell'esito ${n + 1}`), "con prescrizioni");
  // I ruoli che compilano sono righe con un menu, non un elenco di chiavi.
  await page.getByRole("button", { name: "+ Aggiungi un ruolo" }).click();
  await expect(page.getByTestId("ruoli-compilano").getByRole("listitem").first()).toBeVisible();
  await expect(page.getByTestId("ruoli-compilano")).not.toContainText("project_manager");
});

test("import unificati: una pagina, modelli CSV scaricabili", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/configurazione/import");
  await expect(page.getByTestId("import-cosa").getByRole("link")).toHaveCount(4);
  for (const [cosa, file, colonne] of [
    ["applicazioni", "modello-applicazioni.csv", "codice;nome;descrizione;criticita"],
    ["costi", "modello-costi.csv", "elemento;data;importo"],
    ["organizzazione", "modello-organizzazione.csv", "nome;codice;padre"],
    ["lavoro", "modello-lavoro.csv", "chiave;titolo;tipo"],
  ] as const) {
    await page.goto(cosa === "lavoro" ? "/configurazione/import" : `/configurazione/import?cosa=${cosa}`);
    const modello = page.getByTestId("modello-csv");
    await expect(modello).toContainText(colonne);
    const [download] = await Promise.all([page.waitForEvent("download"), modello.getByRole("button", { name: `Scarica ${file}` }).click()]);
    expect(download.suggestedFilename()).toBe(file);
    const stream = await download.createReadStream();
    const chunks: Buffer[] = [];
    for await (const c of stream) chunks.push(c as Buffer);
    const testo = Buffer.concat(chunks).toString("utf8");
    expect(testo.replace(/^﻿/, "").split("\r\n")[0]).toContain(colonne);
  }
  // Le procedure esistenti restano nella stessa pagina.
  await page.goto("/configurazione/import?cosa=applicazioni");
  await expect(page.getByRole("heading", { name: "Applicazioni da CSV" })).toBeVisible();
  await page.goto("/configurazione/import?cosa=organizzazione");
  await expect(page.getByRole("heading", { name: /Import/ }).first()).toBeVisible();
});

test("applicazioni da CSV: prova senza scrivere, poi conferma", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/configurazione/import?cosa=applicazioni");
  const nome = uniq("App da modello");
  const csv = `codice;nome;criticita;team\n;${nome};alta;\n`;
  await page.getByLabel("File CSV delle applicazioni").setInputFiles({ name: "app.csv", mimeType: "text/csv", buffer: Buffer.from(csv) });
  await expect(page.getByText("Prova: 1 nuove, 0 aggiornate.")).toBeVisible();
  await page.getByRole("button", { name: "Conferma l'import" }).click();
  await expect(page.getByText("Importate: 1 nuove, 0 aggiornate.")).toBeVisible();
});

test.describe("in inglese", () => {
  test.use({ locale: "en-US" });

  test("indice, bozza, condizione e import in inglese", async ({ page }) => {
    await login(page, "admin");
    await page.goto("/configurazione");
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.getByRole("heading", { name: "Configuration", level: 1 })).toBeVisible();
    await expect(page.getByTestId("obiettivo-people")).toContainText("People and access");
    await expect(page.getByLabel("Search a setting")).toBeVisible();
    await page.getByLabel("Search a setting").fill("thresholds");
    await expect(page.getByRole("link", { name: "Open DORA thresholds" })).toBeVisible();

    await page.goto("/processi/demand_standard");
    const builder = page.getByRole("group", { name: "Applies when" });
    await expect(builder).toBeVisible();
    await builder.getByRole("button", { name: "+ Add a row" }).click();
    await scegli(builder.getByLabel("Field").first(), /^Driver/);
    await builder.getByLabel("Add a value").first().fill("normativo");
    await builder.getByLabel("Add a value").first().press("Enter");
    await expect(builder.getByTestId("cond-frase")).toContainText(/^Only if/);
    await expect(builder.getByTestId("cond-conteggio")).toContainText(/Today the condition is true for \d+ of \d+ items/);
    const bozza = page.getByTestId("bozza").first();
    await expect(bozza).toContainText("1 unsaved change");
    await expect(bozza.getByRole("button", { name: "Publish", exact: true })).toBeVisible();
    await expect(bozza.getByRole("button", { name: "Discard changes" })).toBeVisible();
    await bozza.getByRole("button", { name: "Discard changes" }).click();

    await page.goto("/configurazione/import?cosa=applicazioni");
    await expect(page.getByText("What are you importing?")).toBeVisible();
    await expect(page.getByRole("button", { name: "Download modello-applicazioni.csv" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "CSV template" })).toBeVisible();
  });
});
