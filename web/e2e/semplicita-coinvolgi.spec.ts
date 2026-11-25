// Ondata «semplicità»: la procedura guidata «Coinvolgi un team» scrive da sola tipo di valutazione,
// regola e nuova versione del processo, con un'anteprima in parole semplici prima di confermare.
import { test, expect } from "@playwright/test";
import { call, expectNoHorizontalScroll, login, scegli, uniq } from "./fixtures";

async function nuovoProcesso(page: import("@playwright/test").Page) {
  const name = uniq("Processo coinvolgi");
  const key = name.toLowerCase().replaceAll(/[^a-z0-9]+/g, "_");
  await call(page.request, "POST", "/api/v1/processes", { key, name, type: "business_request", priority: 907, copy_from: "demand_standard" });
  await call(page.request, "POST", `/api/v1/processes/${key}/publish`, { notes: "di partenza" });
  return { key, name };
}

test("coinvolgi un team in quattro passi: anteprima, conferma, regola pubblicata", async ({ page }) => {
  await login(page, "admin");
  const { key, name } = await nuovoProcesso(page);
  const team = uniq("Cloud broker");
  const before = await call(page.request, "GET", `/api/v1/processes/${key}`);

  // L'ingresso è nella pagina dei team.
  await page.goto("/tempo/team");
  await page.getByRole("link", { name: "Coinvolgi un team" }).click();
  await expect(page).toHaveURL(/\/configurazione\/coinvolgi/);

  // 1. Un team nuovo.
  await page.getByLabel("Un team nuovo: lo creo ora").check();
  await page.getByRole("textbox", { name: "Nome del team" }).fill(team);
  await page.getByRole("button", { name: "Avanti" }).click();

  // 2. Quando: il processo e la fase.
  await scegli(page.getByLabel("Per quale processo"), name);
  await scegli(page.getByLabel("In quale momento"), "Quando la richiesta entra in «Valutazione»");
  await page.getByRole("button", { name: "Avanti" }).click();

  // 3. Cosa fa: parere e lavoro.
  await page.getByLabel("Fare del lavoro").check();
  await page.getByRole("button", { name: "Avanti" }).click();

  // 4. Anteprima in parole semplici; niente è stato scritto.
  await expect(page.getByText("Ecco cosa verrà scritto")).toBeVisible();
  const lista = page.getByRole("list").filter({ hasText: "nuovo tipo di valutazione" });
  await expect(lista).toContainText(`nuovo team «${team}»`);
  await expect(lista).toContainText(`nuova regola nel processo «${name}»: entrando in «Valutazione» apre il parere e crea l'impatto per il team «${team}»`);
  await expect(lista).toContainText(`nuova versione ${before.versions.length + 1} del processo «${name}»`);
  await expect(lista).not.toContainText("open_review");
  expect((await call(page.request, "GET", `/api/v1/processes/${key}`)).versions.length).toBe(before.versions.length);

  // Conferma: tutto scritto, con i link per proseguire.
  await page.getByRole("button", { name: "Conferma e scrivi" }).click();
  await expect(page.getByRole("heading", { name: `Il team «${team}» è coinvolto` })).toBeVisible();
  const dopo = await call(page.request, "GET", `/api/v1/processes/${key}`);
  expect(dopo.versions.length).toBe(before.versions.length + 1);
  const regola = (dopo.current.rules as { key: string; on: string; actions: { kind: string }[] }[]).find((r) => r.key.startsWith("coinvolgi_"))!;
  expect(regola.on).toBe("enter:Valutazione");
  expect(regola.actions.map((a) => a.kind)).toEqual(["open_review", "create_impact"]);

  await expect(page.getByRole("link", { name: "Modifica il questionario" })).toBeVisible();
  await page.getByRole("link", { name: "Vedi la regola" }).click();
  await expect(page).toHaveURL(new RegExp(`/processi/${key}\\?regola=`));
});

test("coinvolgi un team: dal telefono (375 px) i passi stanno in colonna, e dalle valutazioni si arriva alla procedura", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "admin");
  await page.goto("/configurazione/valutazioni");
  await page.getByRole("link", { name: "Coinvolgi un team" }).click();
  await expect(page.getByRole("heading", { name: "Coinvolgi un team" })).toBeVisible();
  await expectNoHorizontalScroll(page);
  // «Avanti» non si può premere finché non c'è un team.
  await expect(page.getByRole("button", { name: "Avanti" })).toBeDisabled();
  await page.getByLabel("Un team nuovo: lo creo ora").check();
  await page.getByRole("textbox", { name: "Nome del team" }).fill(uniq("Team telefono"));
  await page.getByRole("button", { name: "Avanti" }).click();
  await expect(page.getByText("Quando entra in gioco?")).toBeVisible();
  await expectNoHorizontalScroll(page);
});
