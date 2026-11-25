// Test end-to-end dell'interfaccia (pnpm e2e). Playwright è solo una dipendenza di
// sviluppo: non entra nel binario. Il server Regia lo avvia global-setup.ts su una
// cartella dati temporanea, con il seed e i dati demo (vedi docs/sviluppo/test.md).
//
// Variabili:
//   E2E_URL      usa un server già avviato (con seed e dati demo) invece di avviarne uno
//   E2E_PORT     porta del server avviato dai test (default 8140; intervallo 8140-8149)
//   E2E_CHANNEL  browser: "chrome" (default, quello installato) o "chromium" (scaricato
//                con `pnpm exec playwright install chromium`, come in CI)
import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.E2E_PORT || 8140);
const baseURL = process.env.E2E_URL || `http://e2e.localhost:${port}`;
const channel = process.env.E2E_CHANNEL === "chromium" ? undefined : process.env.E2E_CHANNEL || "chrome";

export default defineConfig({
  testDir: "e2e",
  globalSetup: "./e2e/global-setup.ts",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  // I test condividono un solo server: ognuno crea i propri dati con nomi unici.
  fullyParallel: true,
  workers: process.env.CI ? 2 : 4,
  retries: process.env.CI ? 1 : 0,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL,
    locale: "it-IT",
    timezoneId: "Europe/Rome",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chrome", use: { ...devices["Desktop Chrome"], channel, viewport: { width: 1400, height: 900 } } }],
});
