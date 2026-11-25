// Lingua dell'interfaccia, passo 2, area governance (replica/lingua.md): le pagine di
// conformità, rischi, incidente, vulnerabilità e DevSecOps si aprono in inglese e non
// lasciano parole italiane tipiche dell'interfaccia. La lingua viene dal browser
// (Accept-Language), perché la persona non ha scelto: non tocca gli altri test.
import { test, expect, type Locator, type Page } from "@playwright/test";
import { apiAs, call, login, uniq } from "./fixtures";

test.use({ locale: "en-US" });

/** Parole che in inglese non devono più comparire nel testo visibile (sono testi del prodotto, non dati). */
const ITALIANO = /\b(Nessun|Nessuna|Aggiungi|Salva|Annulla|Scadenza|Scadute|Elenco|Vulnerabilità|Conformità|Controlli|Evidenze|Requisiti|Normative|Perimetri|Cruscotto|Fornitori|Incidenti|Rilasci|Politica|Chiudi|Cerca|Tutte|Tutti|Rischi|Applicazioni|Panoramica|Cataloghi|Servizi|Eccezioni|Gravità|Filtri|Mostra|Nascondi|Crea|Esporta|Importa)\b/;

/** Testi che non sono di questa area: arrivano dal server (passo 3) o da componenti comuni di altre aree. */
const ALTROVE = /Requisiti generali|Tutte le altre|Politica di sicurezza|Requisiti di progetto|Fornitori ICT censiti|Nessuna richiesta di change|Servizi critici colpiti|Nessun campo per questo tipo|Nessun collegamento/;

async function noItalian(page: Page, where: string, area: Locator = page.locator("main")) {
  const text = await area.innerText();
  const found = [...text.matchAll(new RegExp(`.{0,25}${ITALIANO.source}.{0,25}`, "g"))].map((m) => m[0].replace(/\s+/g, " ")).filter((x) => !ALTROVE.test(x));
  expect(found, `${where}: parole italiane rimaste`).toEqual([]);
}

test("conformità in inglese: schede, panoramica, controlli e requisiti", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/conformita");
  await expect(page.getByRole("heading", { name: "Compliance", exact: true })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Overview" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Regulations" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Controls" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Evidence" })).toBeVisible();
  await expect(page.getByText("Coverage weighted by criticality")).toBeVisible();
  await noItalian(page, "conformità, panoramica");
  await page.getByRole("tab", { name: "Controls" }).click();
  await expect(page.getByRole("heading", { name: /Internal controls|Controls/ }).first()).toBeVisible();
  await noItalian(page, "conformità, controlli");
  await page.getByRole("tab", { name: "Applicability" }).click();
  await expect(page.getByText("Add a rule")).toBeVisible();
  await noItalian(page, "conformità, applicabilità");
  await page.getByRole("tab", { name: "Catalogs" }).click();
  await expect(page.getByText("Import a catalog")).toBeVisible();
  await noItalian(page, "conformità, cataloghi");
});

test("rischi in inglese: cruscotto, registro, fornitori e incidenti", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/rischi");
  await expect(page.getByText("Risks and incidents").first()).toBeVisible();
  await expect(page.getByRole("tab", { name: "Dashboard" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "ICT providers" })).toBeVisible();
  await noItalian(page, "rischi, cruscotto");
  await page.getByRole("tab", { name: "Risks", exact: true }).click();
  await expect(page.locator("#contenuto .label").filter({ hasText: /^Search$/ })).toBeVisible();
  await noItalian(page, "rischi, registro");
  await page.getByRole("tab", { name: "ICT providers" }).click();
  await expect(page.getByText("New provider")).toBeVisible();
  await noItalian(page, "rischi, fornitori");
  await page.getByRole("tab", { name: "Incidents" }).click();
  await expect(page.getByRole("button", { name: "Report an incident" })).toBeVisible();
  await noItalian(page, "rischi, incidenti");
});

test("incidente in inglese: banda, impatto e notifiche normative", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "incidenti");
  const inc = await call<{ key: string; element: { id: string } }>(req, "POST", "/api/v1/incidents", {
    title: uniq("Incident EN"),
    description: "Created by the end-to-end tests.",
    detected_at: new Date(Date.now() - 2 * 3_600_000).toISOString(),
    clients_affected: 180000,
    clients_affected_pct: 14,
    downtime_minutes: 190,
    economic_impact_cents: 21000000,
    cause: "system",
  });
  await call(req, "POST", `/api/v1/elements/${inc.element.id}/transitions`, { to: "Classificato" });
  await login(page, "incidenti");
  await page.goto(`/e/${inc.key}`);
  const band = page.getByLabel("To do on the incident");
  await expect(band.getByText("Next notification")).toBeVisible();
  await expect(band.getByRole("timer")).toContainText(/left|overdue/);
  await expect(page.getByRole("button", { name: "Prepare the notification" })).toBeVisible();
  const notices = page.getByRole("region", { name: "Regulatory notifications" });
  await expect(notices).toBeVisible();
  await noItalian(page, "incidente, banda", band);
  await noItalian(page, "incidente, notifiche", notices);
});

test("vulnerabilità in inglese: indicatori, filtri ed elenco", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/devsecops/vulnerabilita");
  await expect(page.getByRole("heading", { name: "Vulnerabilities", exact: true })).toBeVisible();
  await expect(page.getByText("Severity", { exact: true })).toBeVisible();
  await expect(page.getByText("Past due").first()).toBeVisible();
  await noItalian(page, "vulnerabilità");
  await page.getByRole("tab", { name: "Exceptions" }).click();
  await expect(page.getByText("Vulnerability exceptions")).toBeVisible();
  await noItalian(page, "vulnerabilità, eccezioni");
});

test("DevSecOps in inglese: pannello, metriche e aderenza della pipeline", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/devsecops");
  await expect(page.getByRole("heading", { name: "DevSecOps", exact: true })).toBeVisible();
  await expect(page.getByText("Critical vulnerabilities in production")).toBeVisible();
  await noItalian(page, "DevSecOps");
  await page.goto("/devsecops/metriche");
  await expect(page.getByRole("heading", { name: "Delivery metrics" })).toBeVisible();
  await noItalian(page, "metriche DORA");
  await page.goto("/devsecops/aderenza");
  await expect(page.getByRole("heading", { name: "Pipeline adherence" })).toBeVisible();
  await noItalian(page, "aderenza");
});

test("rischio, requisiti, architettura e politica della pipeline in inglese", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const risks = await call<{ items: { key: string }[] }>(req, "GET", "/api/v1/risks");
  expect(risks.items.length).toBeGreaterThan(0);
  await login(page, "admin");
  await page.goto(`/e/${risks.items[0].key}?scheda=dettagli`);
  await expect(page.getByText("Inherent", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("Treatment due date")).toBeVisible();
  // La cornice della pagina (schede, campi) è di un'altra area: si guarda solo la scheda dei dettagli.
  await noItalian(page, "dettaglio del rischio", page.getByRole("tabpanel").first());

  await page.goto("/requisiti");
  await expect(page.getByText("To verify").first()).toBeVisible();
  await noItalian(page, "requisiti da verificare");

  await page.goto("/architettura");
  await expect(page.getByRole("heading", { name: "Architecture", exact: true })).toBeVisible();
  await expect(page.getByText("Logical assets").first()).toBeVisible();
  await noItalian(page, "architettura");

  await page.goto("/configurazione/devsecops/politica");
  await expect(page.getByRole("heading", { name: "Pipeline policy", exact: true })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Matrix" })).toBeVisible();
  await noItalian(page, "politica della pipeline");
});
