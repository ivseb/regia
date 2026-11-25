// Accessibilità di base con axe-core (MPL-2.0, solo sviluppo) sulle pagine principali:
// regole WCAG 2.1 A e AA; il test fallisce per le violazioni gravi (serious, critical).
import { test, expect, type Page } from "@playwright/test";
import { createRequire } from "node:module";
import { login } from "./fixtures";

const axePath = createRequire(import.meta.url).resolve("axe-core/axe.min.js");
// La CSP dell'app (script-src 'self') blocca lo script iniettato: solo qui la si ignora.
test.use({ bypassCSP: true });

type Violation = { id: string; impact: string; help: string; nodes: { target: string[]; failureSummary: string }[] };

async function axe(page: Page): Promise<Violation[]> {
  await page.addScriptTag({ path: axePath });
  return page.evaluate(async () => {
    // @ts-expect-error axe è globale dopo addScriptTag
    const r = await window.axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] }, resultTypes: ["violations"] });
    return r.violations.map((v: Violation) => ({ id: v.id, impact: v.impact, help: v.help, nodes: v.nodes.slice(0, 5).map((n) => ({ target: n.target, failureSummary: n.failureSummary })) }));
  });
}

const pages: { who: string; path: string; ready: RegExp }[] = [
  { who: "dm", path: "/richieste", ready: /^Richieste$/ },
  { who: "dm", path: "/richieste?vista=tabella", ready: /^Richieste$/ },
  { who: "dm", path: "/e/DEM-3", ready: /Portale clienti B2B/ },
  { who: "dm", path: "/e/DEM-3?scheda=analisi", ready: /Portale clienti B2B/ },
  { who: "dm", path: "/e/DEM-3?scheda=requisiti", ready: /Portale clienti B2B/ },
  { who: "dm", path: "/richieste/DEM-3/piano", ready: /Portale clienti B2B|Piano/ },
  { who: "sponsor", path: "/approvazioni", ready: /Le mie approvazioni/ },
  { who: "pmo", path: "/cruscotto", ready: /Cruscotto di portafoglio/ },
  { who: "pmo", path: "/graduatoria", ready: /Graduatoria del portafoglio/ },
  { who: "pmo", path: "/roadmap", ready: /Roadmap/ },
  { who: "lead", path: "/delivery/board?spazio=WEB", ready: /Board · Web/ },
  { who: "lead", path: "/delivery/backlog?spazio=WEB", ready: /Backlog/ },
  { who: "sicurezza", path: "/valutazioni", ready: /valutazioni/i },
  { who: "sicurezza", path: "/requisiti", ready: /Da verificare/ },
  { who: "admin", path: "/configurazione/campi", ready: /Campi personalizzati/ },
  { who: "admin", path: "/configurazione/tipi", ready: /Tipi di elemento/ },
  { who: "admin", path: "/processi/demand_standard", ready: /Ciclo di vita della domanda/ },
  { who: "admin", path: "/configurazione/persone", ready: /Persone e accessi/ },
  { who: "dm", path: "/notifiche", ready: /Attività/ },
  { who: "dm", path: "/profilo", ready: /Sara Villa/ },
  { who: "richiedente", path: "/portale", ready: /^Ciao / },
  { who: "richiedente", path: "/portale/nuova?tipo=business_request", ready: /Nuova richiesta/ },
];

for (const p of pages) {
  test(`axe: ${p.path}`, async ({ page }) => {
    await login(page, p.who);
    await page.goto(p.path);
    await expect(page.getByRole("heading", { level: 1, name: p.ready })).toBeVisible();
    await page.waitForLoadState("networkidle");
    const bad = (await axe(page)).filter((v) => v.impact === "serious" || v.impact === "critical");
    expect(bad, JSON.stringify(bad, null, 1)).toEqual([]);
  });
}

test("axe: pagina di accesso", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Accedi" })).toBeVisible();
  const bad = (await axe(page)).filter((v) => v.impact === "serious" || v.impact === "critical");
  expect(bad, JSON.stringify(bad, null, 1)).toEqual([]);
});
