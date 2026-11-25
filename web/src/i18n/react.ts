// Aggancio a React: i componenti si ridisegnano quando cambia la lingua.
import { useSyncExternalStore } from "react";
import { snapshot, subscribe, type Bound } from "./core";

/** La funzione t() della lingua corrente: `const t = useT(); t("auth.login.title")`. */
export function useT(): Bound["t"] {
  return useSyncExternalStore(subscribe, snapshot).t;
}

/** La lingua corrente («it» o «en»). */
export function useLang(): Bound["lang"] {
  return useSyncExternalStore(subscribe, snapshot).lang;
}
