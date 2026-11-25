// Ondata 3, configurazione §5: ambiente di prova e promuovi. Dal tenant «prova» (solo dell'account prova@)
// si crea la prova collegata, si entra (barra «Ambiente di prova» in cima a ogni pagina), si aggiunge una
// fase a un processo, si promuove con «Cosa cambia» e il principale ha la fase nuova; telefono a 375 px.
import { test, expect } from "@playwright/test";
import { call, expectNoHorizontalScroll, login, uniq } from "./fixtures";

test.describe.configure({ mode: "serial" });

test("crea la prova, entra, cambia un processo e promuovi: il principale ha la fase nuova", async ({ page }) => {
  await login(page, "prova", "prova");

  // Un processo pubblicato nel principale, da cambiare nella prova.
  const name = uniq("Processo da provare");
  const key = name.toLowerCase().replaceAll(/[^a-z0-9]+/g, "_");
  await call(page.request, "POST", "/api/v1/processes", { key, name, type: "business_request", priority: 950, copy_from: "demand_standard" });
  await call(page.request, "POST", `/api/v1/processes/${key}/publish`, { notes: "Per la prova" });

  await page.goto("/configurazione/prova");
  await expect(page.getByRole("heading", { name: "Ambiente di prova", level: 1 })).toBeVisible();
  await expect(page.getByTestId("prova-barra")).toHaveCount(0); // nel principale non c'è la barra
  // Cosa si copia e cosa no, detto chiaramente.
  await expect(page.getByText("Non si copia né si promuove:")).toBeVisible();
  if (await page.getByTestId("prova-crea").isVisible()) {
    await page.getByRole("button", { name: "Crea l'ambiente di prova" }).click();
  } else {
    await page.getByRole("button", { name: "Ricrea la prova" }).click();
    await page.getByRole("button", { name: "Sì, ricrea la prova" }).click();
  }
  const linked = page.getByTestId("prova-collegata");
  await expect(linked).toBeVisible();
  await expect(linked).toContainText("Azienda con prova S.r.l. (prova)");

  // Si entra: la barra è in cima e resta su ogni pagina.
  await linked.getByRole("button", { name: "Entra nell'ambiente di prova" }).click();
  const bar = page.getByTestId("prova-barra");
  await expect(bar).toBeVisible();
  await expect(bar).toContainText("Ambiente di prova");
  await page.goto("/richieste");
  await expect(page.getByTestId("prova-barra")).toBeVisible();

  // Nella prova: nuova fase nel processo e pubblicazione, con le pagine normali.
  await page.goto(`/processi/${key}`);
  await page.getByRole("button", { name: "+ Fase" }).click();
  const props = page.getByRole("complementary", { name: "Proprietà dell'elemento selezionato" });
  const phaseName = props.getByLabel(/^Nome/).first();
  await phaseName.fill("Collaudo in prova");
  await phaseName.press("Tab");
  await expect(page.locator("#editor-diagram")).toContainText("Collaudo in prova");
  const publish = page.getByRole("button", { name: /^Pubblica v\d+$/ });
  await expect(publish).toBeEnabled();
  await publish.click();
  await expect(page.getByRole("status").filter({ hasText: /Pubblicata la v\d+/ })).toBeVisible();

  // Il principale non è ancora cambiato (si guarda dall'API della prova stessa tramite il confronto).
  await page.getByTestId("prova-barra").getByRole("link", { name: "Promuovi nel principale" }).click();
  await expect(page).toHaveURL(/\/configurazione\/prova/);
  const voce = page.getByTestId(`prova-voce-process-${key}`);
  await expect(voce).toBeVisible();
  await expect(voce).toHaveAttribute("data-change", "modified");
  await voce.getByRole("button", { name: "Cosa cambia" }).click();
  await expect(voce.getByTestId("prova-cosa-cambia")).toContainText("Collaudo in prova");

  // Promozione.
  await page.getByRole("button", { name: /^Promuovi \d+ ogget/ }).click();
  await expect(page.getByTestId("prova-promosso")).toContainText("nel principale");

  // Si torna al principale: niente barra, e il processo ha la fase nuova in una versione nuova pubblicata.
  await page.getByTestId("prova-barra").getByRole("button", { name: "Torna al principale" }).click();
  await expect(page.getByTestId("prova-barra")).toHaveCount(0);
  const p = await call<{ versions: { number: number; status: string }[]; current: unknown }>(page.request, "GET", `/api/v1/processes/${key}`);
  expect(JSON.stringify(p.current)).toContain("Collaudo in prova");
  expect(p.versions.filter((v) => v.status === "published")).toHaveLength(1);
  expect(p.versions.length).toBeGreaterThanOrEqual(2);

  // Lo storico del principale la mostra.
  await page.goto("/configurazione/prova");
  await expect(page.getByTestId("prova-storico-riga").first()).toContainText("1 oggetto");
});

test("telefono a 375 px: pagina e barra senza scorrimento orizzontale, bersagli da 44 px", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "prova", "prova");
  await page.goto("/configurazione/prova");
  await expect(page.getByTestId("prova-collegata")).toBeVisible();
  await expectNoHorizontalScroll(page);
  await page.getByTestId("prova-collegata").getByRole("button", { name: "Entra nell'ambiente di prova" }).click();
  const bar = page.getByTestId("prova-barra");
  await expect(bar).toBeVisible();
  await expectNoHorizontalScroll(page);
  for (const target of [bar.getByRole("link", { name: "Promuovi nel principale" }), bar.getByRole("button", { name: "Torna al principale" })]) {
    const box = await target.boundingBox();
    expect(box!.height, "bersaglio alto almeno 44 px").toBeGreaterThanOrEqual(43.5);
  }
  await page.goto("/configurazione/prova");
  await expectNoHorizontalScroll(page);
  await bar.getByRole("button", { name: "Torna al principale" }).click();
  await expect(page.getByTestId("prova-barra")).toHaveCount(0);
  await expectNoHorizontalScroll(page);
});
