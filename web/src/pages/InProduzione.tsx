// «In produzione dal…» in salita: per una richiesta, un impatto o un epic, quanti elementi
// discendenti con codice (almeno un commit) sono in produzione e da quando l'ultimo. Si apre
// sull'elenco degli elementi. Senza elementi con codice non mostra niente.
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { rilasciApi } from "../rilasciApi";
import { date } from "../ui";
import { useT } from "../i18n/react";

export function InProduzione({ refKey }: { refKey: string }) {
  const t = useT();
  const q = useQuery({ queryKey: ["element-production", refKey], queryFn: () => rilasciApi.production(refKey), staleTime: 60_000, retry: false });
  const p = q.data;
  if (!p || p.total === 0) return null;
  const line =
    p.in_production === p.total && p.last_at
      ? t("rilasci.prod.tutti", { totale: p.total, data: date(p.last_at) })
      : p.last_at
        ? t("rilasci.prod.salita_ultimo", { n: p.in_production, totale: p.total, data: date(p.last_at) })
        : t("rilasci.prod.salita", { n: p.in_production, totale: p.total });
  return (
    <details className="group flex min-w-0 flex-col gap-1 text-[13px]" data-testid="in-produzione">
      <summary className="cursor-pointer list-none tabular [&::-webkit-details-marker]:hidden">
        <span aria-hidden="true" className="mr-1.5 inline-block w-2 text-muted group-open:rotate-90">›</span>
        {line}
      </summary>
      <ul className="m-0 mt-1 flex list-none flex-col p-0">
        {p.items.map((i) => (
          <li key={i.key} className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-2 border-b border-hair py-1 text-[12.5px] last:border-b-0">
            <Link to="/e/$key" params={{ key: i.key }} className="font-mono text-[12px]">
              {i.key}
            </Link>
            <span className="min-w-0 break-words">
              {i.title}
              <span className="block text-[12px] text-muted">
                {i.production?.in_production && i.production.at ? (
                  i.production.release_id ? (
                    <Link to="/rilasci/$id" params={{ id: i.production.release_id }}>
                      {t("rilasci.prod.dal", { data: date(i.production.at), servizio: i.production.service, versione: i.production.version })}
                    </Link>
                  ) : (
                    t("rilasci.prod.dal", { data: date(i.production.at), servizio: i.production.service, versione: i.production.version })
                  )
                ) : (
                  t("rilasci.prod.in_attesa", { servizi: (i.production?.pending ?? []).join(", ") })
                )}
              </span>
            </span>
          </li>
        ))}
      </ul>
    </details>
  );
}
