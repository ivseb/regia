// Ondata 3: board per team con colonne proprie e modelli (Scrum, Kanban, Supporto). Il modello Kanban si
// applica con l'anteprima, una colonna raccoglie due fasi, una carta si sposta nella colonna, il limite WIP
// superato si legge a parole, un team senza configurazione resta com'era, e a 375 px non si scorre.
import { test, expect } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, memberId, uniq } from "./fixtures";

async function nuovoTeam(req: Parameters<typeof call>[0], prefix: string) {
  const team = uniq(prefix);
  await call(req, "POST", "/api/v1/teams", { name: team });
  const spaces = await call(req, "GET", "/api/v1/work/spaces");
  const space = spaces.items.find((s: { team: string }) => s.team === team).key as string;
  return { team, space };
}

test("modello Kanban con anteprima, colonna a due fasi, spostamento e WIP superato", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const { team, space } = await nuovoTeam(req, "Team kanban");
  const anna = await memberId(req, "Anna Moretti");
  const bugs: { key: string; id: string }[] = [];
  for (const title of ["Errore A", "Errore B"]) {
    const b = await call(req, "POST", `/api/v1/work/spaces/${space}/items`, { type: "bug", title });
    await call(req, "PUT", `/api/v1/work/elements/${b.key}/assignee`, { member_id: anna });
    bugs.push(b);
  }

  await login(page, "admin");
  await page.goto("/tempo/team");
  const sezione = page.getByRole("region", { name: team });
  const colonne = sezione.getByTestId("board-colonne");
  await expect(colonne).toBeVisible();

  // Modello Kanban: l'anteprima prima di applicarlo, la board pubblicata non cambia.
  await colonne.getByRole("button", { name: "Applica un modello" }).click();
  const dialogo = page.getByRole("dialog");
  await dialogo.getByRole("radio", { name: "Modello Kanban" }).click();
  const anteprima = dialogo.getByTestId("anteprima-modello");
  await expect(anteprima).toContainText("Da fare");
  await expect(anteprima).toContainText("In revisione");
  await expect(anteprima).toContainText("Tutte le fasi hanno una colonna.");
  expect((await call(req, "GET", `/api/v1/work/spaces/${space}/board/config`)).published.model ?? "").toBe("");
  await dialogo.getByRole("button", { name: "Applica come bozza" }).click();

  // Editor: toglie «In revisione» (la fase resta senza colonna e lo dice), poi la mette in «In corso»
  // insieme a «Da verificare»: una colonna con più fasi. Limite WIP 1 su «In corso».
  const editor = sezione.getByTestId("editor-colonne");
  await expect(editor).toBeVisible();
  await editor.getByRole("button", { name: "Elimina la colonna In revisione" }).click();
  await expect(editor.getByTestId("fasi-senza-colonna")).toContainText("In revisione");
  const inCorso = editor.getByTestId("colonna-editor").filter({ has: page.locator('input[value="In corso"]') });
  await inCorso.getByRole("combobox", { name: "Fasi della colonna In corso" }).click();
  await page.getByRole("option", { name: /^In revisione/ }).click();
  await page.getByRole("option", { name: /^Da verificare/ }).click();
  await page.keyboard.press("Escape");
  await inCorso.getByLabel("Limite WIP di In corso (0 = nessuno)").fill("1");
  await expect(editor.getByTestId("fasi-senza-colonna")).toHaveCount(0);
  await editor.getByRole("button", { name: "Pubblica" }).click();
  await expect(colonne.getByText("Board pubblicata")).toBeVisible();
  const cfg = await call(req, "GET", `/api/v1/work/spaces/${space}/board/config`);
  expect(cfg.published.model).toBe("kanban");
  expect(cfg.published.no_sprint).toBe(true);
  const colInCorso = cfg.published.columns.find((c: { name: string }) => c.name === "In corso");
  expect(colInCorso.phases).toEqual(expect.arrayContaining(["In corso", "In revisione", "Da verificare"]));
  expect(colInCorso.wip).toBe(1);

  // La board: niente selettore degli sprint (flusso continuo); la carta va in «In corso», la seconda supera il limite.
  await page.goto(`/delivery/board?spazio=${space}`);
  await expect(page.getByRole("heading", { name: new RegExp(`Board · ${team}`) })).toBeVisible();
  await expect(page.getByText("Sprint", { exact: true })).toHaveCount(0);
  const todo = page.getByLabel(/^Da fare: \d+ element[io]$/);
  const doing = page.getByLabel(/^In corso: \d+ element[io]$/);
  await expect(todo).toHaveAccessibleName("Da fare: 2 elementi");
  await page.getByRole("article", { name: new RegExp(`^${bugs[0].key} `) }).dragTo(doing);
  await expect(doing).toHaveAccessibleName("In corso: 1 elemento");
  await expect(page.getByTestId("wip-superato")).toHaveCount(0);
  // La board sposta la card subito; il salvataggio può arrivare un attimo dopo, sotto carico:
  // il secondo spostamento parte solo a salvataggio fatto.
  await expect.poll(async () => (await call(req, "GET", `/api/v1/elements/${bugs[0].key}`)).element.state).toBe("In corso");
  await page.getByRole("article", { name: new RegExp(`^${bugs[1].key} `) }).dragTo(doing);
  await expect(doing).toHaveAccessibleName("In corso: 2 elementi");
  await expect(page.getByTestId("wip-superato")).toContainText("Oltre il limite: 2 su 1");
  await req.dispose();
});

test("un team senza configurazione ha una colonna per fase; a 375 px niente scorrimento orizzontale", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const { team, space } = await nuovoTeam(req, "Team base");
  const board = await call(req, "GET", `/api/v1/work/spaces/${space}/board`);
  expect(board.columns.map((c: { name: string }) => c.name)).toEqual(["Da fare", "In corso", "In revisione", "Da verificare", "Fatto"]);
  expect(board.settings.custom).toBe(false);
  await req.dispose();

  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "admin");
  await page.goto("/tempo/team");
  const colonne = page.getByRole("region", { name: team }).getByTestId("board-colonne");
  await expect(colonne).toContainText("Una colonna per fase del processo");
  await expectNoHorizontalScroll(page);
  await colonne.getByRole("button", { name: "Modifica le colonne" }).click();
  const editor = page.getByTestId("editor-colonne");
  await expect(editor).toBeVisible();
  await expectNoHorizontalScroll(page);
  for (const nome of ["Aggiungi una colonna", "Pubblica", "Salva la bozza"]) {
    const box = await editor.getByRole("button", { name: nome }).boundingBox();
    expect(box!.height, nome).toBeGreaterThanOrEqual(43.5);
  }
  await editor.getByRole("button", { name: "Annulla" }).click();

  // La board sul telefono: una colonna alla volta con il selettore delle colonne.
  await page.goto(`/delivery/board?spazio=${space}&sprint=tutti`);
  await expect(page.getByRole("tablist", { name: "Colonne" })).toBeVisible();
  await expectNoHorizontalScroll(page);
});
