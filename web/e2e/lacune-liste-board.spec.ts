// Lacune del work management: etichette (tag) nelle liste, nelle tabelle e sulle card, e board
// paginata per colonna quando legge tutto lo spazio («Mostra altre»).
import { test, expect } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, newRequest, uniq } from "./fixtures";

async function spazioConElementi(playwright: Parameters<typeof apiAs>[0], baseURL: string, n: number) {
  const { req } = await apiAs(playwright, baseURL, "admin");
  const team = uniq("Team etichette");
  await call(req, "POST", "/api/v1/teams", { name: team });
  const spaces = await call(req, "GET", "/api/v1/work/spaces");
  const space = spaces.items.find((s: { team: string }) => s.team === team).key as string;
  const ids: string[] = [];
  const keys: string[] = [];
  for (let i = 0; i < n; i += 10) {
    const chunk = await Promise.all(
      Array.from({ length: Math.min(10, n - i) }, (_, j) => call(req, "POST", `/api/v1/work/spaces/${space}/items`, { type: "task", title: `Compito ${String(i + j).padStart(3, "0")}` })),
    );
    for (const c of chunk) {
      ids.push(c.id);
      keys.push(c.key);
    }
  }
  return { req, space, ids, keys };
}

test("etichette sulle card e board paginata con «Mostra altre»", async ({ page, playwright, baseURL }) => {
  const { req, space, ids } = await spazioConElementi(playwright, baseURL!, 60);
  const tag = uniq("urgente").replace(/\s+/g, "-");
  await call(req, "POST", "/api/v1/work/bulk", { ids: ids.slice(0, 2), commands: [{ kind: "labels", add: [tag, "beta"] }] });
  await req.dispose();

  await login(page, "admin");
  await page.goto(`/delivery/board?spazio=${space}&sprint=tutti`);
  // I totali sono esatti, le card caricate sono le prime 50 della colonna.
  const todo = page.getByRole("group", { name: /^Da fare: \d+ element[io]$/ });
  await expect(todo).toHaveAccessibleName("Da fare: 50 elementi");
  const header = page.getByText(/^60$/).first();
  await expect(header).toBeVisible();
  // Le etichette compaiono come chip sulla card.
  const first = page.getByRole("article", { name: /Compito 000$/ });
  await expect(first.getByRole("button", { name: `Filtra per l'etichetta ${tag}` })).toBeVisible();
  // Mostra altre (10) carica il resto e il pulsante sparisce.
  const more = page.getByTestId("mostra-altre");
  await expect(more).toHaveText("Mostra altre (10)");
  await more.click();
  await expect(todo).toHaveAccessibleName("Da fare: 60 elementi");
  await expect(more).toHaveCount(0);

  // Il clic su un chip filtra la board per quell'etichetta.
  await todo.evaluate((el) => (el.scrollTop = 0));
  await first.getByRole("button", { name: `Filtra per l'etichetta ${tag}` }).click();
  await expect(page).toHaveURL(new RegExp(`q=.*etichetta`));
  await expect(page.getByRole("group", { name: /^Da fare: \d+ element[io]$/ })).toHaveAccessibleName("Da fare: 2 elementi");
  await expect(page.getByTestId("mostra-altre")).toHaveCount(0);
});

test("etichette nel backlog, nelle richieste e in «Il mio lavoro» a 375 px", async ({ page, playwright, baseURL }) => {
  const { req, space, ids } = await spazioConElementi(playwright, baseURL!, 3);
  const a = uniq("alfa").replace(/\s+/g, "-");
  await call(req, "POST", "/api/v1/work/bulk", { ids, commands: [{ kind: "labels", add: [a, "uno", "due", "tre", "quattro"] }] });
  const richiesta = await newRequest(req, uniq("Richiesta con etichette"));
  await call(req, "POST", "/api/v1/work/bulk", { ids: [richiesta.id], commands: [{ kind: "labels", add: [a] }] });
  await req.dispose();

  await login(page, "admin");
  await page.setViewportSize({ width: 375, height: 800 });

  // Backlog.
  await page.goto(`/delivery/backlog?spazio=${space}`);
  await expect(page.getByRole("button", { name: `Filtra per l'etichetta ${a}` }).first()).toBeVisible();
  await expectNoHorizontalScroll(page);
  await page.getByRole("button", { name: `Filtra per l'etichetta ${a}` }).first().click();
  await expect(page).toHaveURL(/q=.*etichetta/);

  // Richieste: i chip sotto il titolo, «+N» oltre il limite, il clic filtra.
  await page.goto("/richieste?vista=lista");
  await expect(page.getByRole("button", { name: `Filtra per l'etichetta ${a}` }).first()).toBeVisible();
  await expectNoHorizontalScroll(page);
  await page.goto("/richieste?vista=tabella");
  await expect(page.getByRole("button", { name: `Filtra per l'etichetta ${a}` }).first()).toBeVisible();
  await page.getByRole("button", { name: `Filtra per l'etichetta ${a}` }).first().click();
  await expect(page).toHaveURL(/q=.*etichetta/);
  await expect(page.getByRole("link", { name: richiesta.key }).first()).toBeVisible();

  // Il mio lavoro: la pagina resta leggibile.
  await page.goto("/delivery/mio-lavoro");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expectNoHorizontalScroll(page);
});

test("etichette: colonna selezionabile e «+N» nella tabella, intestazione di fase tradotta", async ({ page, playwright, baseURL }) => {
  const { req, ids } = await spazioConElementi(playwright, baseURL!, 1);
  const a = uniq("beta").replace(/\s+/g, "-");
  await call(req, "POST", "/api/v1/work/bulk", { ids, commands: [{ kind: "labels", add: [a, "u", "d", "t", "q"] }] });
  const r = await newRequest(req, uniq("Richiesta etichettata"));
  await call(req, "POST", "/api/v1/work/bulk", { ids: [r.id], commands: [{ kind: "labels", add: [a, "u", "d", "t", "q"] }] });
  await req.dispose();

  await login(page, "admin");
  await page.goto("/richieste?vista=tabella");
  await page.getByRole("button", { name: /^Colonne/ }).click();
  await page.getByRole("checkbox", { name: "Etichette" }).check();
  const riga = page.getByRole("row").filter({ hasText: r.key });
  await expect(riga.getByRole("button", { name: `Filtra per l'etichetta ${a}` })).toBeVisible();
  await expect(riga.locator("[data-tag-more]")).toHaveText("+2");
});
