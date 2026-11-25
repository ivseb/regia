// Barra «Ambiente di prova» in testa a ogni pagina del tenant di prova: ben visibile, per non
// confondere la prova con i dati veri. Vedi internal/api/prova.go.
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { provaApi } from "../provaApi";
import { useEnterTenant } from "./Prova";
import { useT } from "../i18n/react";

export function ProvaBar() {
  const t = useT();
  const state = useQuery({ queryKey: ["prova"], queryFn: provaApi.state, staleTime: 60_000 });
  const enter = useEnterTenant();
  const s = state.data;
  if (!s || s.role !== "prova") return null;
  return (
    <div role="note" className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b-4 border-flag bg-sel px-4 py-2 md:px-7" data-testid="prova-barra">
      <span className="text-[13px] font-bold uppercase tracking-wide">{t("shell.prova.titolo")}</span>
      <span className="text-[13px]">{t("shell.prova.testo")}</span>
      <Link to={"/configurazione/prova" as never} className="touch-44 inline-flex items-center text-[13px] font-semibold text-accent underline">
        {t("shell.prova.promuovi")}
      </Link>
      {s.linked && (
        <button className="btn btn-quiet touch-44 text-[13px]" disabled={enter.isPending} onClick={() => enter.mutate(s.linked!.tenant_id)}>
          {t("shell.prova.torna")}
        </button>
      )}
    </div>
  );
}
