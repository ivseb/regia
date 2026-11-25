// Nomi per lingua delle fasi, per nome: lo stato di un elemento nelle liste arriva come nome, qui si
// trova l'etichetta della lingua corrente (con ripiego sul nome). Vedi docs/sviluppo/etichette-lingua.md.
import { useCallback } from "react";
import { useQuery } from "@tanstack/react-query";
import { call } from "./api";
import { useLang } from "./i18n/react";
import { localName } from "./i18n/localName";

type StateLabelMap = Record<string, Record<string, string>>;

/** `const stateName = useStateName(); stateName(el.state)`: il nome della fase nella lingua corrente. */
export function useStateName(): (name: string, labels?: Record<string, string> | null) => string {
  const lang = useLang();
  const q = useQuery({ queryKey: ["state-labels"], queryFn: () => call<{ items: StateLabelMap }>("GET", "/state-labels"), staleTime: 60_000 });
  const map = q.data?.items;
  return useCallback((name, labels) => localName(name, labels ?? map?.[name], lang), [map, lang]);
}
