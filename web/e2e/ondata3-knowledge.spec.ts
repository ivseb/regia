// Ondata 3: editor a blocchi del Knowledge (blocchi con «/», maniglia, barra sulla selezione, modo Markdown)
// e pannello «Questo documento è evidenza di…». Il formato salvato resta Markdown. Telefono a 375 px.
import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, scegli, uniq } from "./fixtures";

async function newPage(req: APIRequestContext, title: string, body = "") {
  const space = "K" + Math.random().toString(36).slice(2, 6).toUpperCase();
  await call(req, "POST", "/api/v1/kb/spaces", { key: space, name: uniq("Spazio blocchi") });
  const doc = await call<{ page: { key: string } }>(req, "POST", `/api/v1/kb/spaces/${space}/pages`, { title, body });
  return doc.page.key;
}

const bodyOf = async (req: APIRequestContext, key: string) => (await call<{ page: { body: string } }>(req, "GET", `/api/v1/kb/pages/${key}`)).page.body;
const editor = (page: Page) => page.getByRole("group", { name: "Editor a blocchi" });

test("blocchi con «/», spostamento con Alt+↓, grassetto dalla barra, salvataggio in Markdown e ritorno dai modi", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const key = await newPage(req, uniq("Pagina a blocchi"));
  await login(page, "admin");
  await page.goto(`/documenti/${key}?vista=modifica`);
  const ed = editor(page);
  await expect(ed).toBeVisible();

  await ed.getByRole("textbox", { name: "Paragrafo" }).first().click();
  await page.keyboard.type("/titolo 1");
  await expect(page.getByRole("listbox", { name: "Inserisci un blocco" }).getByRole("option")).toHaveCount(1);
  await page.keyboard.press("Enter");
  await page.keyboard.type("Registro degli accessi");
  await page.keyboard.press("Enter");
  await page.keyboard.type("/elenco p");
  await page.keyboard.press("Enter");
  await page.keyboard.type("Primo");
  await page.keyboard.press("Enter");
  await page.keyboard.type("Secondo");
  await page.keyboard.press("Enter");
  await page.keyboard.press("Enter"); // Invio su una voce vuota: si esce dall'elenco
  await page.keyboard.type("/avviso: att");
  await page.keyboard.press("Enter");
  await page.keyboard.type("Fermare il job");

  // Sposta «Primo» sotto «Secondo» con Alt+↓.
  await ed.getByRole("textbox", { name: /Elenco puntato/ }).first().click();
  await page.keyboard.press("Alt+ArrowDown");
  await expect(ed.getByRole("textbox", { name: /Elenco puntato/ }).first()).toHaveText("Secondo");
  await expect(ed.getByRole("textbox", { name: /Elenco puntato/ }).nth(1)).toHaveText("Primo");

  // Grassetto dalla barra sulla selezione.
  await ed.getByRole("textbox", { name: "Avviso: attenzione" }).dblclick({ position: { x: 20, y: 8 } });
  const bar = page.getByRole("toolbar", { name: "Formattazione del testo selezionato" });
  await expect(bar).toBeVisible();
  await bar.getByRole("button", { name: "Grassetto" }).click();
  await expect(ed.getByRole("textbox", { name: "Avviso: attenzione" }).locator("strong, b")).toHaveText("Fermare");

  await page.getByRole("button", { name: "Salva la versione" }).click();
  await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
  await expect.poll(() => bodyOf(req, key)).toBe("# Registro degli accessi\n\n- Secondo\n- Primo\n\n> [!ATTENZIONE] **Fermare** il job");

  // Modo Markdown e ritorno ai blocchi.
  await page.goto(`/documenti/${key}?vista=modifica`);
  await page.getByRole("button", { name: "Markdown", exact: true }).click();
  await expect(page.getByRole("textbox", { name: /Testo della pagina|Corpo/ }).or(page.locator("textarea").first())).toHaveValue("# Registro degli accessi\n\n- Secondo\n- Primo\n\n> [!ATTENZIONE] **Fermare** il job");
  await expect(page.getByRole("button", { name: "Grassetto" })).toBeVisible(); // etichette leggibili, non sigle
  await expect(page.getByRole("button", { name: "Elenco di controllo" })).toBeVisible();
  await page.getByRole("button", { name: "Blocchi", exact: true }).click();
  await expect(editor(page).getByRole("textbox", { name: "Titolo 1" })).toHaveText("Registro degli accessi");
  await expect(editor(page).getByRole("textbox", { name: "Avviso: attenzione" }).locator("strong, b")).toHaveText("Fermare");
  await req.dispose();
});

test("Backspace all'inizio unisce al blocco precedente; il menu del blocco cambia tipo, duplica ed elimina", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const key = await newPage(req, uniq("Pagina unire"), "Alfa\n\nBeta");
  await login(page, "admin");
  await page.goto(`/documenti/${key}?vista=modifica`);
  const ed = editor(page);
  const beta = ed.getByRole("textbox", { name: "Paragrafo" }).nth(1);
  await expect(beta).toHaveText("Beta");
  await beta.click();
  await page.keyboard.press("Home");
  await page.keyboard.press("Backspace");
  await expect(ed.getByRole("textbox", { name: "Paragrafo" }).first()).toHaveText("AlfaBeta");

  await ed.getByRole("button", { name: /trascina per spostare/ }).first().click();
  await page.getByRole("menuitemradio", { name: "Titolo 2" }).click();
  await expect(ed.getByRole("textbox", { name: "Titolo 2" })).toHaveText("AlfaBeta");
  await ed.getByRole("button", { name: /Titolo 2: trascina/ }).first().click();
  await page.getByRole("menuitem", { name: "Duplica" }).click();
  await expect(ed.getByRole("textbox", { name: "Titolo 2" })).toHaveCount(2);
  await ed.getByRole("button", { name: /Titolo 2: trascina/ }).first().click();
  await page.getByRole("menuitem", { name: "Elimina il blocco" }).click();
  await expect(ed.getByRole("textbox", { name: "Titolo 2" })).toHaveCount(1);
  await req.dispose();
});

test("pannello «Questo documento è evidenza di…»: collega un controllo e lo mostra tra i controlli", async ({ page, playwright, baseURL }) => {
  const co = await apiAs(playwright, baseURL!, "conformita");
  const owner = co.me.active_tenant!.member_id;
  const ctitle = uniq("Controllo accessi");
  const ctl = await call<{ key: string }>(co.req, "POST", "/api/v1/grc/controls", { title: ctitle, text: "Controllo per il pannello evidenza.", owner_id: owner });
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const key = await newPage(req, uniq("Procedura accessi"), "## Scopo\n\nChi può entrare.\n\n## Regole\n\nSolo con badge.");
  await login(page, "admin");
  await page.goto(`/documenti/${key}`);
  const panel = page.getByTestId("evidence-panel");
  await expect(panel.getByRole("heading", { name: "Questo documento è evidenza di…" })).toBeVisible();
  await panel.getByRole("button", { name: "Collega" }).click();
  await scegli(panel.getByRole("combobox", { name: "Controllo, applicazione, rischio o altro elemento" }), new RegExp(ctl.key), { cerca: ctl.key });
  await expect(panel.getByText("Controlli", { exact: true })).toBeVisible();
  await expect(panel.getByRole("link", { name: new RegExp(ctl.key) })).toBeVisible();
  // Il sommario sta accanto al testo su schermi larghi.
  await expect(page.getByRole("navigation", { name: "In questa pagina" })).toBeVisible();
  await panel.getByRole("button", { name: `Scollega ${ctl.key}` }).click();
  await expect(panel.getByRole("link", { name: new RegExp(ctl.key) })).toHaveCount(0);
  await co.req.dispose();
  await req.dispose();
});

test("telefono a 375 px: blocchi a tutta larghezza, pulsante menu del blocco da 44 px, niente scorrimento orizzontale", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const key = await newPage(req, uniq("Pagina telefono"), "# Titolo\n\nUn testo abbastanza lungo da andare a capo più volte quando lo schermo è stretto come quello di un telefono.\n\n| a | b | c | d |\n|---|---|---|---|\n| uno | due | tre | quattro |\n\n```\nuna riga di codice molto lunga che non deve far scorrere la pagina in orizzontale nemmeno a 375 px\n```");
  await login(page, "admin");
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto(`/documenti/${key}?vista=modifica`);
  const ed = editor(page);
  await expect(ed).toBeVisible();
  await expect(ed.getByRole("button", { name: /trascina per spostare/ })).toHaveCount(0);
  const par = ed.getByRole("textbox", { name: "Paragrafo" }).first();
  await par.click();
  const menu = ed.getByRole("button", { name: /Menu del blocco: Paragrafo/ });
  await expect(menu).toBeVisible();
  const box = await menu.boundingBox();
  expect(box!.width).toBeGreaterThanOrEqual(44);
  expect(box!.height).toBeGreaterThanOrEqual(44);
  const pb = await par.boundingBox();
  expect(pb!.width, "il paragrafo occupa quasi tutta la larghezza").toBeGreaterThan(375 - 16 * 2 - 60);
  await expectNoHorizontalScroll(page);
  await menu.click();
  await expect(page.getByRole("menu", { name: "Azioni del blocco" })).toBeVisible();
  await page.getByRole("menuitemradio", { name: "Elenco puntato" }).click();
  await expect(ed.getByRole("textbox", { name: /Elenco puntato/ })).toBeVisible();
  await expectNoHorizontalScroll(page);
  await req.dispose();
});
