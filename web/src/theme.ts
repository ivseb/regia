// Modo chiaro/scuro: "system" segue il sistema operativo; la scelta manuale resta nel browser.
// Su <html> data-theme vale sempre "light" o "dark" (themes.css non ha media query): con
// "system" lo si ricalcola quando il sistema cambia.
export type Theme = "system" | "light" | "dark";
const KEY = "regia.theme";

function stored(): string | null {
  try {
    return localStorage.getItem(KEY);
  } catch {
    return null;
  }
}

export function getTheme(): Theme {
  const v = stored();
  return v === "light" || v === "dark" ? v : "system";
}

const media = typeof matchMedia === "function" ? matchMedia("(prefers-color-scheme: dark)") : null;
let current: Theme = "system";

function follow() {
  if (current === "system") document.documentElement.dataset.theme = media?.matches ? "dark" : "light";
}
media?.addEventListener("change", follow);

export function applyTheme(t: Theme) {
  current = t;
  try {
    if (t === "system") localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, t);
  } catch {
    // Archivio del browser non disponibile: la scelta vale fino al prossimo caricamento.
  }
  if (t === "system") follow();
  else document.documentElement.dataset.theme = t;
}
