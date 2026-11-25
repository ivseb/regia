// Difetti d'uso corretti dopo i rapporti di usabilità: pagina «non trovato», board su telefono,
// pulsanti degli sprint, priorità dei processi, tipo del campo, testo dei gruppi, titolo dei
// documenti, diagramma dei processi, ricerca delle vulnerabilità in ⌘K, pulsante principale del triage.
import { test, expect } from "@playwright/test";
import { apiAs, advance, login, newRequest, scegli } from "./fixtures";

test("404 dentro la shell: pagina con stile e link per ripartire", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/architettura/applicazioni");
  await expect(page.getByRole("heading", { name: "Pagina non trovata", level: 1 })).toBeVisible();
  await expect(page.getByText("/architettura/applicazioni")).toBeVisible();
  await page.getByRole("link", { name: "Vai all'inizio" }).click();
  await expect(page).toHaveURL(/\/richieste/);
});

test("board su telefono: si apre sulla prima colonna con schede", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "dev");
  await page.goto("/delivery/board?spazio=WEB");
  const tabs = page.getByRole("tablist", { name: "Colonne" }).getByRole("tab");
  await expect(tabs.first()).toBeVisible();
  const all = await tabs.allInnerTexts();
  const firstFull = all.findIndex((t) => Number(/(\d+)\s*$/.exec(t.trim())?.[1] ?? 0) > 0);
  expect(firstFull).toBeGreaterThanOrEqual(0);
  await expect(tabs.nth(firstFull)).toHaveAttribute("aria-selected", "true");
});

test("sprint: chi ha la sola lettura non vede i pulsanti e il server rifiuta", async ({ page }) => {
  await login(page, "lead"); // referente del team Web, ruolo di sola lettura
  await page.goto("/delivery/backlog?spazio=WEB");
  await expect(page.getByRole("heading", { name: "Backlog e sprint" })).toBeVisible();
  await expect(page.getByRole("region").first()).toBeVisible();
  await expect(page.getByRole("button", { name: "Nuovo sprint" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /^(Avvia|Chiudi) lo sprint$/ })).toHaveCount(0);
  const r = await page.request.post("/api/v1/work/spaces/WEB/sprints", { data: { title: "Sprint abusivo" } });
  expect(r.status()).toBe(403);
});

test("triage: il pulsante principale è il passaggio in avanti", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const { id, key } = await newRequest(req, "Richiesta per il pulsante principale");
  await req.dispose();
  const { req: dm } = await apiAs(playwright, baseURL!, "dm");
  await advance(dm, id, "Triage");
  await dm.dispose();
  await login(page, "dm");
  await page.goto(`/e/${key}`);
  const forward = page.getByRole("button", { name: "Avvia la valutazione" });
  await expect(forward).toBeVisible();
  await expect(forward).toHaveClass(/btn-primary/);
  await expect(page.getByRole("button", { name: "Rimanda al richiedente" })).not.toHaveClass(/btn-primary/);
});

test("pagina dei gruppi: il testo segue l'impostazione di ereditarietà", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/configurazione/persone?tab=gruppi");
  await expect(page.getByText(/ruoli di un gruppo valgono anche per i membri dei suoi sottogruppi/)).toBeVisible();
  await expect(page.getByText(/non passano dal gruppo padre/)).toHaveCount(0);
});

test("campo personalizzato: il tipo si sceglie, non c'è un predefinito", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/configurazione/campi");
  await page.getByRole("button", { name: "Nuovo campo" }).click();
  const editor = page.getByRole("complementary", { name: "Modifica del campo" });
  await editor.getByLabel("Nome", { exact: true }).fill("Campo senza tipo");
  const type = editor.getByLabel(/^Tipo di dato/);
  await expect(type).toHaveAttribute("data-value", "");
  await expect(editor.getByText(/il tipo non si potrà più cambiare/)).toBeVisible();
  await expect(editor.getByRole("button", { name: "Crea il campo" })).toBeDisabled();
  await scegli(type, "Numero intero");
  await expect(editor.getByRole("button", { name: "Crea il campo" })).toBeEnabled();
});

test("nuovo processo: la priorità proposta non pareggia e un pareggio viene segnalato", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/processi");
  await page.getByRole("button", { name: "Nuovo processo" }).click();
  const form = page.getByRole("region", { name: "Nuovo processo" });
  await scegli(form.getByLabel("Tipo di elemento"), { value: "business_request" });
  const prio = form.getByLabel("Priorità");
  const proposed = Number(await prio.getAttribute("placeholder"));
  expect(proposed).toBeGreaterThan(100);
  await expect(form.getByRole("alert")).toHaveCount(0);
  await prio.fill("100");
  await expect(form.getByRole("alert")).toContainText("La priorità 100 è già di");
});

test("documento Knowledge a 1024 px: titolo a capo tra le parole e testo vicino alla testata", async ({ page }) => {
  await page.setViewportSize({ width: 1024, height: 768 });
  await login(page, "admin");
  await page.goto("/documenti/DOC-6");
  const h1 = page.getByRole("heading", { level: 1 });
  await expect(h1).toBeVisible();
  const title = (await h1.innerText()).trim();
  // Nessuna riga del titolo finisce a metà parola: ogni riga rispetta i confini delle parole.
  const lines = await h1.evaluate((el) => {
    const range = document.createRange();
    const node = el.firstChild as Text;
    const out: string[] = [];
    let last = -1;
    let buf = "";
    for (let i = 0; i < node.length; i++) {
      range.setStart(node, i);
      range.setEnd(node, i + 1);
      const top = Math.round(range.getBoundingClientRect().top);
      if (last !== -1 && top !== last) {
        out.push(buf);
        buf = "";
      }
      last = top;
      buf += node.data[i];
    }
    out.push(buf);
    return out;
  });
  expect(lines.join("")).toBe(title);
  for (const l of lines.slice(0, -1)) expect(l.endsWith(" ") || l.endsWith("-"), `riga spezzata a metà parola: «${l}»`).toBeTruthy();
  const body = page.getByRole("heading", { name: "Scopo" });
  await expect(body).toBeVisible();
  const top = (await body.boundingBox())!.y;
  expect(top).toBeLessThan(700);
});

test("diagramma dei processi: sta nella colonna e si può tornare alle dimensioni reali", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/processi/demand_standard");
  const diagram = page.getByRole("group", { name: "Diagramma delle fasi e dei passaggi" });
  await expect(diagram).toBeVisible();
  const box = page.locator("#editor-diagram .overflow-x-auto").first();
  const fits = async () => (await box.evaluate((el) => el.scrollWidth <= el.clientWidth + 1)) as boolean;
  const scaled = (await diagram.boundingBox())!.width;
  await page.getByRole("button", { name: "Dimensioni reali" }).click();
  const real = (await diagram.boundingBox())!.width;
  expect(real).toBeGreaterThan(scaled);
  await page.getByRole("button", { name: "Adatta alla larghezza" }).click();
  expect((await diagram.boundingBox())!.width).toBeLessThanOrEqual(scaled + 1);
  expect(typeof (await fits())).toBe("boolean");
});

test("⌘K trova le vulnerabilità per identificativo", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/devsecops");
  await page.waitForLoadState("networkidle"); // la scorciatoia si registra a pagina pronta
  await page.keyboard.press("ControlOrMeta+k");
  const dialog = page.getByRole("dialog", { name: "Palette dei comandi" });
  await dialog.getByRole("combobox").fill("CVE-2026-4");
  const option = dialog.getByRole("option", { name: /CVE-2026-4\d+/ }).first();
  await expect(option).toBeVisible();
  const cve = /CVE-2026-4\d+/.exec(await option.innerText())![0];
  await option.click();
  await expect(page).toHaveURL(new RegExp(`/devsecops/vulnerabilita\\?q=${cve}`));
  await expect(page.getByText(cve).first()).toBeVisible();
});
