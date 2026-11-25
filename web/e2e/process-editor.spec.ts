// Editor dei processi: nuovo processo copiato da quello dei bug (priorità bassa, così non si
// applica agli elementi degli altri test), si aggiunge una fase e si pubblica.
import { test, expect } from "@playwright/test";
import { call, login, uniq, scegli } from "./fixtures";

test("editor dei processi: aggiungi una fase e pubblica", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/processi");
  await page.getByRole("button", { name: "Nuovo processo" }).click();
  const form = page.getByRole("region", { name: "Nuovo processo" });
  await form.getByLabel("Copiando un processo esistente").check();
  await scegli(form.getByLabel("Processo da copiare"), "Ciclo di vita del bug · Bug");
  const name = uniq("Bug con collaudo");
  await form.getByLabel("Nome", { exact: true }).fill(name);
  await form.getByLabel("Priorità").fill("900");
  await form.getByRole("button", { name: "Crea e apri l'editor" }).click();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
  const key = /\/processi\/([^/?#]+)/.exec(page.url())![1];

  // Nuova fase: "+ Fase", poi il nome nel pannello delle proprietà.
  await page.getByRole("button", { name: "+ Fase" }).click();
  const props = page.getByRole("complementary", { name: "Proprietà dell'elemento selezionato" });
  const phaseName = props.getByLabel(/^Nome/).first();
  await phaseName.fill("Collaudo utente");
  await phaseName.press("Tab");
  await expect(page.locator("#editor-diagram")).toContainText("Collaudo utente");
  await expect(page.getByText("nessun errore")).toBeVisible();
  await page.getByLabel("Note della versione").fill("Aggiunta la fase di collaudo utente.");
  const publish = page.getByRole("button", { name: /^Pubblica v\d+$/ });
  await expect(publish).toBeEnabled();
  const version = Number((await publish.innerText()).replace(/\D/g, ""));
  await publish.click();
  await expect(page.getByRole("status").filter({ hasText: `Pubblicata la v${version}` })).toBeVisible();

  // La versione pubblicata contiene la fase nuova.
  const p = await call(page.request, "GET", `/api/v1/processes/${key}`);
  const current = JSON.stringify(p);
  expect(current).toContain("Collaudo utente");
});
