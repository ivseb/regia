// Import CSV delle ore dal timesheet: nella pagina Import (Costi), il bottone «Ore» cambia destinazione; si prova un
// file con una riga buona e una sbagliata, si conferma ignorando l'errore, le ore compaiono sull'elemento e si annullano.
import { test, expect } from "@playwright/test";
import { apiAs, call, login, newRequest, scegli, uniq } from "./fixtures";

test("importa le ore da un CSV: prova con l'errore di una riga, conferma, ore visibili, annullamento", async ({ page, playwright, baseURL }) => {
  const req = (await apiAs(playwright, baseURL!, "finanza")).req;
  const r = await newRequest(req, uniq("Portale ore"));
  const nome = uniq("Ore timesheet");
  await call(req, "POST", "/api/v1/finance/import-profiles", {
    name: nome,
    target: "work_logs",
    mapping: {
      columns: { Attività: "element", Chi: "member", Giorno: "work_date", Ore: "hours", Riga: "source_ref" },
      delimiter: ";",
      date_format: "GG/MM/AAAA",
      decimal: ",",
    },
  });
  const rif = uniq("T").replace(/\s/g, "");
  const csv =
    "Attività;Chi;Giorno;Ore;Riga\n" +
    `${r.key};finanza@demo.test;04/03/2024;1,5;${rif}-1\n` + // buona: 90 minuti
    `${r.key};nessuno@demo.test;04/03/2024;2;${rif}-2\n`; // la persona non è un membro

  await login(page, "finanza");
  await page.goto("/configurazione/import?cosa=costi");
  await page.getByTestId("import-target").getByRole("button", { name: "Ore" }).click();
  await expect(page.getByTestId("import-target").getByRole("button", { name: "Ore" })).toHaveAttribute("aria-pressed", "true");

  await scegli(page.getByRole("combobox", { name: "Profilo di mappatura" }), nome);
  await page.getByLabel("File CSV").setInputFiles({ name: "ore.csv", mimeType: "text/csv", buffer: Buffer.from(csv) });
  await page.getByRole("button", { name: "Prova senza salvare" }).click();

  // L'errore della riga 3 dice quale campo e perché; la riga buona è in anteprima con le sue ore.
  const esito = page.locator("section").filter({ hasText: "Esito di ore.csv" });
  await expect(esito).toBeVisible();
  await expect(esito.getByText(/non è un membro dell'organizzazione/)).toBeVisible();
  await expect(esito.getByRole("cell", { name: r.key })).toBeVisible();
  await expect(esito.getByRole("cell", { name: "1h 30m" })).toBeVisible();

  // Con un errore serve ignorarlo; poi si conferma.
  const conferma = page.getByRole("button", { name: /^Conferma l'import di 1 righe/ });
  await expect(conferma).toBeDisabled();
  await page.getByLabel(/Importa le righe valide e ignora le 1 con errori/).check();
  await conferma.click();

  // Le ore sono sull'elemento (90 minuti = 1,5 ore) e l'import compare tra i recenti, annullabile.
  await expect
    .poll(async () => {
      const { items } = await call<{ items: { hours: number }[] }>(req, "GET", `/api/v1/finance/work-logs?element=${r.key}`);
      return items.reduce((n, x) => n + x.hours, 0);
    }, { timeout: 15_000 })
    .toBe(1.5);
  const recenti = page.getByRole("row", { name: /ore\.csv/ }).first();
  await expect(recenti).toContainText(/importato/);
  await recenti.getByRole("button", { name: "Annulla" }).click();
  await expect
    .poll(async () => (await call<{ items: unknown[] }>(req, "GET", `/api/v1/finance/work-logs?element=${r.key}`)).items.length, { timeout: 15_000 })
    .toBe(0);
});
