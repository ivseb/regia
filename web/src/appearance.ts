// Aspetto: tema (direzione visiva, themes.css) e coppia di font. Si applica con due
// attributi su <html> (data-skin, data-font); il chiaro/scuro resta in theme.ts.
// La scelta effettiva arriva dal server (/me) e si copia nel browser, così al prossimo
// caricamento si applica subito, prima che React disegni: niente sfarfallio.
import { ApiError } from "./api";

export type ThemeKey = "atlante" | "petrolio" | "nebbia" | "contrasto";
export type FontKey = "atkinson" | "plex" | "source" | "public";
export type Appearance = { theme: ThemeKey; font: FontKey };
/** Scelta parziale: null = segue il livello sopra (organizzazione, poi prodotto). */
export type AppearanceChoice = { theme: ThemeKey | null; font: FontKey | null };

// Le chiavi devono coincidere con internal/api/appearance.go. Le descrizioni stanno nel catalogo
// (profile.theme.<chiave>, profile.font.<chiave>).
export const THEMES: { key: ThemeKey; name: string; swatch: { light: string[]; dark: string[] } }[] = [
  {
    key: "atlante",
    name: "Atlante", // i18n-ok: nome di ripiego, la lingua legge profile.theme.<chiave>
    swatch: { light: ["#ffffff", "#0a0a0a", "#2b3ff2", "#c2410c"], dark: ["#0c0c0e", "#f4f4f5", "#8b9bff", "#ff8f5a"] },
  },
  {
    key: "petrolio",
    name: "Petrolio", // i18n-ok: nome di ripiego, la lingua legge profile.theme.<chiave>
    swatch: { light: ["#faf7f1", "#1c1b18", "#0b5550", "#a6430c"], dark: ["#121513", "#ece7dd", "#6cc3b5", "#f2995f"] },
  },
  {
    key: "nebbia",
    name: "Nebbia", // i18n-ok: nome di ripiego, la lingua legge profile.theme.<chiave>
    swatch: { light: ["#f3f5f7", "#1f2933", "#2d5282", "#a8430d"], dark: ["#1b2027", "#e2e7ed", "#9dbae6", "#f4a06a"] },
  },
  {
    key: "contrasto",
    name: "Contrasto", // i18n-ok: nome di ripiego, la lingua legge profile.theme.<chiave>
    swatch: { light: ["#ffffff", "#000000", "#0030b0", "#8f3300"], dark: ["#000000", "#ffffff", "#ffe14d", "#ffa45c"] },
  },
];

// family: il nome della famiglia del testo, per aspettare che il file sia arrivato.
export const FONTS: { key: FontKey; name: string; mono: string; family: string; load: () => Promise<unknown> }[] = [
  {
    key: "atkinson",
    name: "Atkinson Hyperlegible",
    mono: "Atkinson Hyperlegible Mono",
    family: "Atkinson Hyperlegible Next Variable",
    load: () => import("./fonts/atkinson"),
  },
  {
    key: "plex",
    name: "IBM Plex Sans",
    mono: "IBM Plex Mono",
    family: "IBM Plex Sans Variable",
    load: () => import("./fonts/plex"),
  },
  {
    key: "source",
    name: "Source Sans 3",
    mono: "Source Code Pro",
    family: "Source Sans 3 Variable",
    load: () => import("./fonts/source"),
  },
  {
    key: "public",
    name: "Public Sans",
    mono: "JetBrains Mono",
    family: "Public Sans Variable",
    load: () => import("./fonts/public"),
  },
];

export const DEFAULT_APPEARANCE: Appearance = { theme: "atlante", font: "atkinson" };

const isTheme = (v: unknown): v is ThemeKey => THEMES.some((t) => t.key === v);
const isFont = (v: unknown): v is FontKey => FONTS.some((f) => f.key === v);
export const themeName = (k: string | null | undefined) => THEMES.find((t) => t.key === k)?.name ?? k ?? "";
export const fontName = (k: string | null | undefined) => FONTS.find((f) => f.key === k)?.name ?? k ?? "";

const KEY = "regia.appearance";

/** L'ultima scelta effettiva salvata nel browser (o il default di prodotto). */
export function storedAppearance(): Appearance {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? "{}");
    return { theme: isTheme(v.theme) ? v.theme : DEFAULT_APPEARANCE.theme, font: isFont(v.font) ? v.font : DEFAULT_APPEARANCE.font };
  } catch {
    return DEFAULT_APPEARANCE;
  }
}

const loaded = new Map<FontKey, Promise<void>>();

/**
 * Carica la coppia di font (CSS e file restano nel binario) e aspetta il testo regolare,
 * al massimo per `waitMs`: oltre, il font arriva da solo (font-display: swap).
 */
export function loadFont(key: FontKey, waitMs = 600): Promise<void> {
  const f = FONTS.find((x) => x.key === key) ?? FONTS[0];
  let p = loaded.get(f.key);
  if (!p) {
    p = f
      .load()
      .then(() => document.fonts?.load(`400 1em "${f.family}"`))
      .then(() => undefined)
      .catch(() => undefined);
    loaded.set(f.key, p);
  }
  return Promise.race([p, new Promise<void>((r) => setTimeout(r, waitMs))]);
}

let applied = 0;

/** Applica l'aspetto a tutta l'interfaccia e lo ricorda nel browser. */
export function applyAppearance(a: Partial<Appearance> | null | undefined): Promise<void> {
  const next: Appearance = {
    theme: isTheme(a?.theme) ? a.theme : DEFAULT_APPEARANCE.theme,
    font: isFont(a?.font) ? a.font : DEFAULT_APPEARANCE.font,
  };
  const root = document.documentElement;
  const done = loadFont(next.font);
  const mine = ++applied;
  // Il font nuovo prima del cambio di famiglia, se arriva in fretta: niente salto di testo.
  void done.then(() => {
    if (mine === applied) root.dataset.font = next.font;
  });
  root.dataset.skin = next.theme;
  try {
    localStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    // Archivio del browser non disponibile: si applica solo a questa pagina.
  }
  return done;
}

// ---------------------------------------------------------------------
// API (appearance.go)
// ---------------------------------------------------------------------

export type Preferences = {
  appearance: AppearanceChoice;
  tenant_default: AppearanceChoice | null;
  effective: Appearance;
  options: { themes: string[]; fonts: string[] };
};
export type TenantAppearance = AppearanceChoice & { updated_at: string | null; updated_by: string | null };

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    method,
    credentials: "same-origin",
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, data.code ?? "unknown", data.detail ?? res.statusText, data.field);
  return data as T;
}

export const appearanceApi = {
  preferences: () => call<Preferences>("GET", "/me/preferences"),
  savePreferences: (appearance: AppearanceChoice) => call<Preferences>("PUT", "/me/preferences", { appearance }),
  tenant: () => call<TenantAppearance>("GET", "/tenant/appearance"),
  saveTenant: (a: AppearanceChoice) => call<TenantAppearance>("PUT", "/tenant/appearance", a),
};

/**
 * Tiene l'interfaccia allineata alla scelta del server: GET /me porta l'aspetto effettivo;
 * le risposte di login e cambio di organizzazione no, e allora lo si chiede a parte.
 */
export function followServerAppearance(me: unknown) {
  if (!me || typeof me !== "object") return;
  const a = (me as { appearance?: Appearance }).appearance;
  if (a) {
    void applyAppearance(a);
    return;
  }
  appearanceApi
    .preferences()
    .then((p) => applyAppearance(p.effective))
    .catch(() => undefined);
}
