// Lingue dell'interfaccia. Il nome è sempre nella lingua stessa, così si riconosce anche
// quando l'interfaccia è in un'altra. Le chiavi coincidono con internal/api/language.go.
import type { Lang } from "./types";

export const LANGUAGES: { key: Lang; name: string; locale: string }[] = [
  { key: "it", name: "Italiano", locale: "it-IT" },
  { key: "en", name: "English", locale: "en-US" },
];

export const DEFAULT_LANG: Lang = "it";

export const isLang = (v: unknown): v is Lang => LANGUAGES.some((l) => l.key === v);

/** Prima lingua supportata tra quelle del browser (come Accept-Language), altrimenti l'italiano. */
export function browserLang(prefs: readonly string[] = typeof navigator === "undefined" ? [] : (navigator.languages ?? [navigator.language])): Lang {
  for (const p of prefs) {
    const base = p.toLowerCase().split("-")[0];
    if (isLang(base)) return base;
  }
  return DEFAULT_LANG;
}
