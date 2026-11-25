// Stato vuoto delle liste di un'organizzazione senza nemmeno un elemento di quel tipo: una frase e
// l'azione giusta, al posto della barra della query e della tabella vuota.
import { Link } from "@tanstack/react-router";
import { EmptyState } from "../emptyState";
import { useMe } from "../shell";
import { useT } from "../i18n/react";
import type { Key } from "../i18n/core";

type Def = { to?: string; search?: Record<string, string>; perm?: string };

/** I tipi che hanno uno stato vuoto, e dove porta il pulsante. */
const DEFS: Record<string, Def> = {
  business_request: { to: "/richieste/nuova", perm: "element.write" },
  risk: { to: "/rischi", search: { tab: "rischi" }, perm: "element.write" },
  incident: { to: "/rischi", search: { tab: "incidenti" } },
  control: { to: "/conformita", search: { tab: "controlli" } },
  page: { to: "/documenti" },
  application: { to: "/architettura", search: { tab: "applicazioni" } },
  logical_asset: { to: "/architettura", search: { tab: "asset" } },
  impact: {},
};

export const hasEmptyState = (typeKey: string) => typeKey in DEFS;

export function ListaVuota({ typeKey }: { typeKey: string }) {
  const t = useT();
  const me = useMe().data;
  const perms = me?.active_tenant?.permissions ?? [];
  const has = (p?: string) => !p || perms.includes("*") || perms.includes(p);
  const admin = perms.includes("*") || perms.includes("tenant.admin");
  const d = DEFS[typeKey];
  if (!d) return null;
  return (
    <div className="flex flex-col gap-3 px-4 py-4 md:px-7" data-testid="lista-vuota">
      <EmptyState
        title={t(`lavoro.vuoti.${typeKey}.titolo` as Key)}
        text={t(`lavoro.vuoti.${typeKey}.testo` as Key)}
        action={
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
            {d.to && has(d.perm) && (
              <Link to={d.to as never} search={d.search as never} className="btn btn-primary no-underline">
                {t(`lavoro.vuoti.${typeKey}.azione` as Key)}
              </Link>
            )}
            {admin && (
              <Link to="/configurazione/avvio" search={{ tab: "modelli" }} className="touch-44 inline-flex items-center text-accent underline">
                {t("lavoro.vuoti.modello")}
              </Link>
            )}
          </div>
        }
      />
    </div>
  );
}
