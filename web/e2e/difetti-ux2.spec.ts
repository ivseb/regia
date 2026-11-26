// Seconda tornata di difetti d'uso (replica/usabilita.md, sezione 2): titoli della scheda del
// browser, percorsi in minuscolo, salto al contenuto, menu del revisore, editor dei processi
// (fase nuova prima di «Fatto», gate nel pannello del passaggio), ruoli ereditati dai gruppi,
// carico della graduatoria e della roadmap a 375 px, caricamento con scheletro.
import { test, expect } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, uniq, scegli } from "./fixtures";

test("titolo della scheda del browser: la pagina, non solo «Regia»", async ({ page }) => {
  await login(page, "pmo");
  await page.goto("/graduatoria");
  await expect(page.getByRole("heading", { level: 1, name: /Graduatoria del portafoglio/ })).toBeVisible();
  await expect(page).toHaveTitle("Graduatoria · Regia");
  await page.goto("/delivery/board?spazio=WEB");
  await expect(page).toHaveTitle("Board dei team · Regia");
});

test("percorsi in minuscolo su tutte le pagine e salto al contenuto", async ({ page }) => {
  await login(page, "sponsor");
  await page.goto("/approvazioni");
  await expect(page.getByRole("heading", { name: "Le mie approvazioni" })).toBeVisible();
  await expect(page.getByText("demand / approvazioni", { exact: true })).toBeVisible();
  await page.goto("/richieste");
  await expect(page.getByText("demand / richieste", { exact: true })).toBeVisible();
  // Il primo Tab porta al link «Salta al contenuto», che sposta il fuoco sul contenuto.
  await page.keyboard.press("Tab");
  const skip = page.getByRole("link", { name: "Salta al contenuto" });
  await expect(skip).toBeFocused();
  await expect(skip).toBeInViewport();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/#contenuto$/);
});

test("il revisore (sola lettura) non vede le voci per creare e registrare", async ({ page }) => {
  await login(page, "revisore");
  await page.goto("/richieste");
  const nav = page.getByRole("navigation", { name: "Navigazione principale" });
  // Il profilo del revisore (ruolo Auditor) lascia solo i moduli di controllo: niente richieste né delivery.
  await expect(nav.getByRole("link", { name: "Rischi e conformità", exact: true })).toBeVisible();
  await expect(nav.getByRole("link", { name: "Richieste", exact: true })).toHaveCount(0);
  await expect(nav.getByRole("link", { name: "Nuova richiesta" })).toHaveCount(0);
  await expect(nav.getByRole("link", { name: "Timesheet" })).toHaveCount(0);
  // Gli elenchi filtrati stanno dietro una voce sola, «Elenchi»: «Documenti» resta una voce unica.
  await expect(nav.getByRole("link", { name: "Elenchi", exact: true })).toBeVisible();
  await expect(nav.getByRole("link", { name: "Documenti", exact: true })).toHaveCount(1);
});

test("i controlli dei campi del filtro hanno nomi leggibili", async ({ page }) => {
  await login(page, "dm");
  await page.goto("/richieste");
  await page.getByRole("button", { name: /Aggiungi filtro|Filtro/ }).first().click();
  await expect(page.getByText("Unità", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("Unita", { exact: true })).toHaveCount(0);
  await expect(page.getByText("Scadenza sla", { exact: true })).toHaveCount(0);
});

test("editor dei processi: la fase nuova va prima di quella finale e il primo passaggio va avanti; gate nel pannello", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/processi");
  await page.getByRole("button", { name: "Nuovo processo" }).click();
  const form = page.getByRole("region", { name: "Nuovo processo" });
  await form.getByLabel("Copiando un processo esistente").check();
  await scegli(form.getByLabel("Processo da copiare"), "Ciclo di vita del bug · Bug");
  const name = uniq("Bug con fase");
  await form.getByLabel("Nome", { exact: true }).fill(name);
  await form.getByLabel("Priorità").fill("901");
  await form.getByRole("button", { name: "Crea e apri l'editor" }).click();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
  const key = /\/processi\/([^/?#]+)/.exec(page.url())![1];

  // Nessuna fase selezionata: «+ Fase» non la mette dopo la fase finale.
  await page.getByRole("button", { name: "+ Fase" }).click();
  const props = page.getByRole("complementary", { name: "Proprietà dell'elemento selezionato" });
  const phaseName = props.getByLabel(/^Nome/).first();
  await phaseName.fill("Collaudo qui");
  await phaseName.press("Tab");
  await props.getByRole("button", { name: "+ Nuovo passaggio da questa fase" }).click();
  // Il passaggio parte dalla fase nuova e porta a una fase finale, non indietro all'iniziale.
  const to = props.getByRole("combobox").first(); // «A»
  await expect(to).toBeVisible();
  const toValue = (await to.getAttribute("data-value")) ?? "";
  const detail = await call(page.request, "GET", `/api/v1/processes/${key}`);
  const text = JSON.stringify(detail);
  const phases: { name: string; category: string; off_path?: boolean }[] = /"phases":(\[[^\]]*\])/.exec(text) ? JSON.parse(/"phases":(\[[^\]]*\])/.exec(text)![1]) : [];
  const final = phases.filter((p) => p.category === "done" && !p.off_path).map((p) => p.name);
  expect(final.length).toBeGreaterThan(0);
  expect(final).toContain(toValue);

  // Un gate nato dal passaggio si compila nel pannello del passaggio, che resta a vista.
  await props.getByRole("button", { name: "+ Nuovo gate per questo passaggio" }).click();
  const gate = props.getByRole("group", { name: /Gate «Nuovo gate»/ });
  await expect(gate).toBeVisible();
  await gate.getByLabel("Nome").fill("Collaudo firmato");
  // Il titolo del gruppo segue il nome: si continua dal pannello.
  await props.getByLabel("Messaggio quando non è superato").fill("Serve il collaudo firmato");
  await expect(props.getByRole("group", { name: /Gate «Collaudo firmato»/ })).toBeVisible();
  await expect(props.getByLabel("Messaggio quando non è superato")).toHaveValue("Serve il collaudo firmato");
});

test("gruppi: il sottogruppo mostra i ruoli ereditati dal gruppo padre", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const roles = await call(req, "GET", "/api/v1/admin/roles");
  const viewer = roles.items.find((r: { key: string | null }) => r.key === "viewer");
  expect(viewer).toBeTruthy();
  const padre = await call(req, "POST", "/api/v1/admin/groups", { name: uniq("Padre"), parent_id: null });
  const nome = uniq("Figlio");
  const figlio = await call(req, "POST", "/api/v1/admin/groups", { name: nome, parent_id: padre.id });
  await call(req, "POST", `/api/v1/admin/roles/${viewer.id}/assignments`, { group_id: padre.id });
  const sec = await call(req, "GET", "/api/v1/admin/security");
  await req.dispose();
  await login(page, "admin");
  await page.goto(`/configurazione/persone?tab=gruppi&sel=${figlio.id}`);
  await expect(page.getByRole("heading", { name: nome, level: 2 })).toBeVisible();
  if (sec.group_inheritance) {
    await expect(page.getByText(/ereditato da Padre/)).toBeVisible();
    await expect(page.getByText("Nessuno assegnato direttamente.")).toBeVisible();
  } else {
    await expect(page.getByText(/ereditato da/)).toHaveCount(0);
  }
});

test.describe("a 375 px", () => {
  test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

  test("le approvazioni stanno in pagina senza scorrimento laterale", async ({ page }) => {
    await login(page, "sponsor");
    await page.goto("/approvazioni");
    await expect(page.getByRole("heading", { name: "Le mie approvazioni" })).toBeVisible();
    await page.waitForLoadState("networkidle");
    await expectNoHorizontalScroll(page);
    // La tabella non ha più una larghezza minima: nessun riquadro che scorre di lato.
    const wide = await page.evaluate(() => {
      const t = document.querySelector("main table") as HTMLElement | null;
      return t ? t.scrollWidth - (t.parentElement?.clientWidth ?? 0) : 0;
    });
    expect(wide).toBeLessThanOrEqual(1);
  });

  test("graduatoria: la colonna «Cumulata» si legge intera", async ({ page }) => {
    await login(page, "pmo");
    await page.goto("/graduatoria");
    await expect(page.getByRole("heading", { level: 1, name: /Graduatoria del portafoglio/ })).toBeVisible();
    await page.waitForLoadState("networkidle");
    await expectNoHorizontalScroll(page);
    const cut = await page.evaluate(() => {
      const th = [...document.querySelectorAll("th")].find((x) => x.textContent?.trim() === "Cumulata") as HTMLElement | undefined;
      if (!th) return -1;
      const box = th.getBoundingClientRect();
      return Math.ceil(box.right - document.documentElement.clientWidth);
    });
    expect(cut, "«Cumulata» deve stare dentro lo schermo").toBeLessThanOrEqual(0);
  });

  test("roadmap: i titoli a sinistra si leggono interi invece di essere troncati", async ({ page }) => {
    await login(page, "pmo");
    await page.goto("/roadmap");
    await expect(page.getByRole("heading", { level: 1, name: /Roadmap/ })).toBeVisible();
    await page.waitForLoadState("networkidle");
    // Nessun taglio (ellissi o righe limitate): il titolo va a capo quanto serve.
    const cut = await page.evaluate(() => {
      const a = document.querySelector("main a[href*='/piano']") as HTMLElement | null;
      if (!a) return "manca il titolo";
      const cs = getComputedStyle(a);
      return cs.textOverflow === "ellipsis" || a.scrollHeight > a.clientHeight + 1 ? `tagliato (${cs.webkitLineClamp})` : "";
    });
    expect(cut).toBe("");
    await expectNoHorizontalScroll(page);
  });
});

test("caricamento con scheletro e testo per i lettori di schermo", async ({ page }) => {
  await login(page, "dm");
  // Si rallenta la scheda: il caricamento è visibile, non una pagina vuota.
  await page.route("**/api/v1/elements/DEM-3", async (route) => {
    await new Promise((r) => setTimeout(r, 700));
    await route.continue();
  });
  await page.goto("/e/DEM-3");
  const loading = page.getByRole("status").filter({ hasText: "Caricamento…" }).first();
  await expect(loading).toBeAttached();
  await expect(loading.locator(".animate-pulse")).toBeVisible();
  await expect(page.getByRole("heading", { name: /Portale clienti B2B/, level: 1 })).toBeVisible();
});
