// Board: si trascina un elemento; il gate "serve un assegnatario" la blocca e la riporta al suo
// posto con il motivo; assegnata, lo spostamento riesce.
import { test, expect } from "@playwright/test";
import { apiAs, call, login, uniq, scegli } from "./fixtures";

test("board: sposta un elemento, con il blocco del gate", async ({ page, playwright, baseURL }) => {
  // Team e spazio propri: gli altri test (e i limiti WIP delle board demo) non interferiscono.
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const team = uniq("Team e2e");
  await call(req, "POST", "/api/v1/teams", { name: team });
  const spaces = await call(req, "GET", "/api/v1/work/spaces");
  const space = spaces.items.find((s: { team: string }) => s.team === team).key as string;
  const story = await call(req, "POST", `/api/v1/work/spaces/${space}/items`, { type: "story", title: "Ricerca per codice fiscale", fields: { punti: 3 } });
  await req.dispose();

  await login(page, "admin");
  await page.goto(`/delivery/board?spazio=${space}&sprint=tutti`);
  await expect(page.getByRole("heading", { name: new RegExp(`Board · ${team}`) })).toBeVisible();
  const card = page.getByRole("article", { name: `${story.key} Ricerca per codice fiscale` });
  const todo = page.getByLabel(/^Da fare: \d+ element[io]$/);
  const doing = page.getByLabel(/^In corso: \d+ element[io]$/);
  await expect(todo).toHaveAccessibleName("Da fare: 1 elemento");

  // Senza assegnatario il gate blocca: avviso con il motivo e l'elemento resta in "Da fare".
  await card.dragTo(doing);
  await expect(page.getByRole("alert")).toContainText(`${story.key} non spostata: serve un assegnatario per iniziare`);
  await expect(todo).toHaveAccessibleName("Da fare: 1 elemento");
  await expect(doing).toHaveAccessibleName("In corso: 0 elementi");
  await page.getByRole("alert").getByRole("button", { name: "Chiudi" }).click();

  // Si assegna dal menu dell'elemento e si riprova.
  await page.getByRole("button", { name: `Azioni su ${story.key}` }).click();
  await scegli(page.getByRole("dialog").getByLabel("Assegnatario"), "Anna Moretti");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await card.dragTo(doing);
  await expect(page.getByRole("status").filter({ hasText: `${story.key} → In corso` })).toBeVisible();
  await expect(doing).toHaveAccessibleName("In corso: 1 elemento");
  const { req: check } = await apiAs(playwright, baseURL!, "admin");
  expect((await call(check, "GET", `/api/v1/elements/${story.key}`)).element.state).toBe("In corso");
  await check.dispose();
});
