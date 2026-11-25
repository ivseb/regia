// Banda in testa all'incidente (replica/usabilita.md, sezione 4): la prossima notifica normativa
// con il conto alla rovescia, e l'invito a portare avanti o chiudere l'incidente quando ha una
// data di risoluzione ma la fase è ferma. Sta sopra le schede, quindi si vede subito anche sul
// telefono; i pulsanti hanno un bersaglio di almeno 44 px.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type ElementDetail } from "../api";
import { riskApi, type Notification } from "../riskApi";
import { ErrorNote, date } from "../ui";
import { Link } from "@tanstack/react-router";
import { Countdown, dateTime } from "./riskUi";
import { tr } from "../i18n/core";

/** Giorni dopo i quali un incidente risolto e fermo in una fase non finale si segnala come da chiudere. */
const STALLED_DAYS = 1;

export function nextNotification(items: Notification[]): Notification | undefined {
  return items
    .filter((n) => (n.status === "pending" || n.status === "draft") && n.due_at)
    .sort((a, b) => new Date(a.due_at!).getTime() - new Date(b.due_at!).getTime())[0];
}

export function IncidentBand({ refKey, detail, onOpen }: { refKey: string; detail: ElementDetail; onOpen: () => void }) {
  const qc = useQueryClient();
  const view = useQuery({ queryKey: ["risk", "incident", refKey], queryFn: () => riskApi.incident(refKey) });
  const move = useMutation({
    mutationFn: (to: string) => api.transition(detail.element.id, to),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["risk"] });
      void qc.invalidateQueries({ queryKey: ["element", refKey] });
      void qc.invalidateQueries({ queryKey: ["activity"] });
    },
  });
  const x = view.data;
  if (!x) return null;
  const next = nextNotification(x.notifications);
  const waiting = x.notifications.filter((n) => n.status === "pending" || n.status === "draft").length;
  const done = detail.element.state_category === "done" || detail.element.state_category === "cancelled";
  const days = x.resolved_at ? (Date.now() - new Date(x.resolved_at).getTime()) / 86_400_000 : 0;
  const stalled = !!x.resolved_at && !done && days >= STALLED_DAYS;
  const forward = detail.transitions.filter((t) => t.forward && t.blocked.length === 0 && !t.comment_required);
  if (!next && !stalled && !(waiting > 0) && done) return null;
  return (
    <div className="flex flex-col border-t border-hair" aria-label={tr("governance.incidentband.da_fare_sull_incidente")}>
      {!done && (
        <div className="flex items-center gap-3 px-4 py-2 md:px-7">
          <Link to="/crisi/$key" params={{ key: refKey }} className="inline-flex min-h-[44px] items-center font-semibold md:min-h-[24px]">
            {tr("crisi.band.open")}
          </Link>
          <span className="text-[12.5px] text-muted">{tr("crisi.band.hint")}</span>
        </div>
      )}
      {next ? (
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-l-4 border-flag bg-sel px-4 py-3 md:px-7">
          <div className="flex min-w-0 flex-[1_1_16rem] flex-col gap-0.5">
            <span className="label text-flag">{tr("governance.incidentband.prossima_notifica")}</span>
            <span className="text-[14px] font-semibold">
              {next.scheme_name.split(" · ")[0]} · {next.stage_name}
            </span>
            <span className="text-[12.5px] text-muted tabular">{tr("governance.riskdetail.entro", { dateTime: dateTime(next.due_at) })}{waiting > 1 ? tr("governance.incidentband.altre_da_inviare", { p: waiting - 1 }) : ""}
            </span>
          </div>
          <span className="text-[17px]">
            <Countdown due={next.due_at} />
          </span>
          <button
            className="btn btn-primary min-h-[44px] justify-center"
            onClick={() => {
              onOpen();
              window.setTimeout(() => document.getElementById("notifiche")?.scrollIntoView({ behavior: "smooth", block: "start" }), 150);
            }}
          >{tr("governance.incidentband.prepara_la_notifica")}</button>
        </div>
      ) : waiting > 0 ? (
        <p className="m-0 border-l-4 border-flag bg-sel px-4 py-3 text-[13px] md:px-7">
          {waiting} {waiting === 1 ? tr("governance.incidentband.notifica_attende") : tr("governance.incidentband.notifiche_attendono")}{" "}{tr("governance.incidentband.la_tappa_precedente_la_scadenza")}</p>
      ) : null}
      {stalled && (
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-l-4 border-rule bg-sel px-4 py-3 md:px-7">
          <p className="m-0 min-w-0 flex-[1_1_16rem] text-[13px]">
            <span className="font-semibold">{tr("governance.incidentband.risolto_il", { date: date(x.resolved_at!) })}</span>{tr("governance.incidentband.ma_l_incidente_e_ancora", { state: detail.element.state })}</p>
          {forward.length > 0 && (
            <span className="flex flex-wrap gap-2">
              {forward.map((t) => (
                <button key={t.to} className="btn min-h-[44px] justify-center" disabled={move.isPending} onClick={() => move.mutate(t.to)}>
                  {t.label}
                </button>
              ))}
            </span>
          )}
        </div>
      )}
      <ErrorNote error={move.error} />
    </div>
  );
}
