// Striscia «Inizia da qui» in testa alle pagine per l'amministratore: resta finché i passi non sono tutti
// fatti; a passi completi si può richiudere (e riaprire dalla pagina). Lo stato lo calcola il server.
import { Link, useRouterState } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { avvioApi } from "../avvioApi";
import type { Me } from "../api";
import { useT } from "../i18n/react";

export function AvvioStrip({ me }: { me: Me }) {
  const perms = me.active_tenant?.permissions ?? [];
  const admin = perms.includes("*") || perms.includes("tenant.admin");
  const path = useRouterState({ select: (s) => s.location.pathname });
  if (!admin || path.startsWith("/configurazione/avvio") || path.startsWith("/configurazione/percorso")) return null;
  return <Strip />;
}

function Strip() {
  const t = useT();
  const qc = useQueryClient();
  const state = useQuery({ queryKey: ["avvio"], queryFn: avvioApi.state, staleTime: 60_000 });
  const close = useMutation({ mutationFn: avvioApi.close, onSuccess: (data) => qc.setQueryData(["avvio"], data) });
  const s = state.data;
  if (!s || s.closed) return null;
  const complete = s.done === s.total;
  return (
    <div role="note" className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-hair bg-sel/40 px-4 py-2 md:px-7" data-testid="avvio-striscia">
      <span className="text-[13px]">{complete ? t("configurazione.avvio.striscia.completo") : t("configurazione.avvio.striscia.testo", { done: s.done, total: s.total })}</span>
      <Link to="/configurazione/avvio" className="touch-44 inline-flex items-center text-[13px] font-semibold text-accent underline">
        {t("configurazione.avvio.striscia.apri")}
      </Link>
      {complete && (
        <button className="btn btn-quiet text-[13px]" disabled={close.isPending} onClick={() => close.mutate()}>
          {t("configurazione.avvio.richiudi")}
        </button>
      )}
    </div>
  );
}
