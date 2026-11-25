// Testo con pezzi di JSX dentro: `<Trans k="auth.invite.invitedAs" slots={{ tenant: <b>{x}</b> }} />`.
// Il catalogo scrive {tenant} come un parametro qualsiasi; i `slots` lo sostituiscono con un
// elemento, i `params` con un testo. L'ordine delle parole lo decide la traduzione.
import { Fragment, type ReactNode } from "react";
import type { Key, Params } from "./core";
import { useT } from "./react";

export function Trans({ k, params, slots }: { k: Key; params?: Params; slots?: Record<string, ReactNode> }) {
  const t = useT();
  // Si lascia {nome} dove c'è uno slot, poi lo si scambia con l'elemento.
  const keep: Params = { ...params };
  for (const name of Object.keys(slots ?? {})) keep[name] = `{${name}}`;
  const text = t(k, keep);
  const parts = text.split(/(\{\w+\})/g);
  return (
    <>
      {parts.map((p, i) => {
        const m = /^\{(\w+)\}$/.exec(p);
        return m && slots && m[1] in slots ? <Fragment key={i}>{slots[m[1]]}</Fragment> : <Fragment key={i}>{p}</Fragment>;
      })}
    </>
  );
}
