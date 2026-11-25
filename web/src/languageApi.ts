// Lingua dell'interfaccia (internal/api/language.go): scelta del membro e predefinita del tenant.
import { call, type LanguageInfo } from "./api";
import type { Lang } from "./i18n/types";

export const languageApi = {
  get: () => call<LanguageInfo>("GET", "/me/language"),
  /** locale null = segue il tenant. */
  setMine: (locale: Lang | null) => call<LanguageInfo>("PUT", "/me/language", { locale }),
  /** locale null = nessun predefinito (vale la lingua del browser). Solo tenant.admin. */
  setTenant: (locale: Lang | null) => call<LanguageInfo>("PUT", "/tenant/language", { locale }),
};
