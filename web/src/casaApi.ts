// Chiamate della casa della configurazione: dati e conteggio dal vivo del costruttore di condizioni.
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { call, type FactInfo } from "./api";

export type CondCount = {
  ok: boolean;
  error?: string;
  matched: number;
  total: number;
  /** Più elementi di quelli valutati: il numero è «almeno». */
  truncated?: boolean;
  items: { key: string; title: string }[];
};

const q = encodeURIComponent;

export const casaApi = {
  /** Dati usabili nelle condizioni sugli elementi di un tipo (stessi dell'editor dei processi). */
  facts: (type: string) => call<{ facts: FactInfo[]; sample?: string }>("GET", `/conditions/facts?type=${q(type)}`),
  /** Quanti elementi del tipo (o di tutti i tipi, se vuoto) soddisfano oggi la condizione. */
  count: (condition: string, type: string) => call<CondCount>("POST", "/conditions/count", { condition, type }),
};

/** Dati del costruttore per un tipo di elemento; vuoto finché non arrivano. */
export function useTypeFacts(type: string | undefined): { facts: FactInfo[]; sample?: string } {
  const r = useQuery({ queryKey: ["cond-facts", type], queryFn: () => casaApi.facts(type!), enabled: !!type, staleTime: 60_000 });
  return { facts: r.data?.facts ?? [], sample: r.data?.sample || undefined };
}

/** Il valore, ma solo dopo `ms` senza cambiamenti (per non chiamare il server a ogni tasto). */
export function useDebounced<T>(value: T, ms = 400): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return v;
}
