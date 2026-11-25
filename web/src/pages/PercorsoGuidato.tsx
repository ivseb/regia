// Configurazione → Percorso guidato: le aree da configurare in ordine sensato, come un sentiero a tappe.
// Lo stato di ogni tappa lo calcola il server dai dati veri del tenant (internal/api/avvio_percorso.go);
// funziona su un'organizzazione nuova (si parte dai modelli di «Inizia da qui») e su una già attiva
// (mostra cosa manca e cosa non torna). Resta raggiungibile anche dopo aver chiuso «Inizia da qui».
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { avvioApi, type IssueCode, type Percorso, type Tappa, type TappaKey } from "../avvioApi";
import { PageHeader } from "../shell";
import { ErrorNote, Loading } from "../ui";
import { useT } from "../i18n/react";
import type { Key } from "../i18n/core";

type Dest = { to: string; search?: Record<string, string> };

/** Dove porta ogni tappa. */
const TAPPA_LINK: Record<TappaKey, Dest> = {
  persone: { to: "/configurazione/persone" },
  team: { to: "/architettura", search: { tab: "team" } },
  processi: { to: "/processi" },
  valutazioni: { to: "/configurazione/valutazioni" },
  spazi: { to: "/documenti" },
  moduli: { to: "/configurazione/moduli" },
};

/** Le tappe in cui un modello di partenza aiuta. */
const HAS_MODEL: TappaKey[] = ["processi", "valutazioni", "moduli"];

/** Dove si sistema ogni incoerenza. */
const ISSUE_LINK: Record<IssueCode, Dest> = {
  team_senza_membri: { to: "/architettura", search: { tab: "team" } },
  team_senza_spazio: { to: "/architettura", search: { tab: "team" } },
  processo_non_pubblicato: { to: "/processi" },
  processo_di_partenza: { to: "/processi" },
  valutazione_team_senza_membri: { to: "/configurazione/valutazioni", search: { tab: "team" } },
  valutazione_senza_regola: { to: "/configurazione/valutazioni", search: { tab: "tipi" } },
  regola_valutazione_mancante: { to: "/processi" },
  approvazione_senza_politica: { to: "/processi" },
  spazio_senza_responsabile: { to: "/documenti" },
};

const STATUS_CLASS: Record<string, string> = {
  done: "border-ok bg-ok text-bg",
  partial: "border-flag text-flag",
  todo: "border-rule text-muted",
};

export function PercorsoGuidato() {
  const t = useT();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["avvio", "percorso"], queryFn: avvioApi.percorso });
  const toggle = useMutation({
    mutationFn: (close: boolean) => (close ? avvioApi.close() : avvioApi.reopen()),
    onSuccess: (data) => {
      qc.setQueryData(["avvio"], data);
      void qc.invalidateQueries({ queryKey: ["avvio", "percorso"] });
    },
  });
  return (
    <>
      <PageHeader
        path={t("percorso.path")}
        title={t("percorso.title")}
        actions={
          <Link to="/mappa-processi" className="btn no-underline">
            {t("percorso.mappa")}
          </Link>
        }
      />
      <div className="flex max-w-[860px] flex-col gap-5 px-4 pb-12 md:px-7">
        <p className="m-0 max-w-3xl text-muted">{t("percorso.intro")}</p>
        {q.isPending && <Loading />}
        {!q.isPending && !q.data && <ErrorNote error={q.error} />}
        {q.data && (
          <>
            {q.data.fresh && (
              <section className="flex flex-col gap-2 border-2 border-rule p-4" data-testid="percorso-nuova">
                <h2 className="m-0 text-[18px] font-bold">{t("percorso.fresh.title")}</h2>
                <p className="m-0 text-muted">{t("percorso.fresh.text")}</p>
                <div>
                  <Link to="/configurazione/avvio" search={{ tab: "modelli" }} className="btn btn-primary no-underline">
                    {t("percorso.fresh.button")}
                  </Link>
                </div>
              </section>
            )}
            <Progress p={q.data} />
            <ol className="m-0 flex list-none flex-col p-0" aria-label={t("percorso.sezioni")}>
              {q.data.tappe.map((tp, i) => (
                <TappaRow key={tp.key} tappa={tp} index={i} last={i === q.data.tappe.length - 1} />
              ))}
            </ol>
            <div className="flex flex-wrap items-center gap-3 border-t border-hair pt-4">
              <Link to="/configurazione/avvio" className="btn no-underline">
                {t("percorso.inizia")}
              </Link>
              {q.data.closed ? (
                <>
                  <span className="text-muted" role="status">{t("percorso.chiusa")}</span>
                  <button className="btn" disabled={toggle.isPending} onClick={() => toggle.mutate(false)}>
                    {t("percorso.riapri")}
                  </button>
                </>
              ) : (
                <button className="btn btn-quiet" disabled={toggle.isPending} onClick={() => toggle.mutate(true)}>
                  {t("percorso.chiudi")}
                </button>
              )}
            </div>
            <ErrorNote error={toggle.error} />
          </>
        )}
      </div>
    </>
  );
}

function Progress({ p }: { p: Percorso }) {
  const t = useT();
  const next = p.tappe.find((x) => x.key === p.next);
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-semibold tabular" data-testid="percorso-progress">{t("percorso.progress", { done: p.done, total: p.total })}</span>
        <div className="h-2 min-w-32 flex-1 bg-sel" aria-hidden="true">
          <div className="h-2 bg-ok" style={{ width: `${p.total ? (p.done / p.total) * 100 : 0}%` }} />
        </div>
      </div>
      {/* Il sentiero in sintesi: una tappa dopo l'altra, da sinistra a destra (solo da tablet in su). */}
      <ol className="m-0 hidden list-none items-start p-0 md:flex" aria-label={t("percorso.stripAria")} data-testid="percorso-sentiero">
        {p.tappe.map((tp, i) => (
          <li key={tp.key} className="flex min-w-0 flex-1 flex-col gap-1">
            <div className="flex items-center">
              <a
                href={`#tappa-${tp.key}`}
                aria-label={t(`percorso.tappa.${tp.key}.title` as Key)}
                className={`inline-flex size-7 shrink-0 items-center justify-center border font-mono text-[12.5px] no-underline ${STATUS_CLASS[tp.status]}`}
              >
                {tp.status === "done" ? "✓" : i + 1}
              </a>
              {i < p.tappe.length - 1 && <span aria-hidden="true" className={`h-0.5 flex-1 ${tp.status === "done" ? "bg-ok" : "bg-hair"}`} />}
            </div>
            <span className="pr-2 text-[12.5px] leading-tight text-muted">{t(`percorso.tappa.${tp.key}.title` as Key)}</span>
          </li>
        ))}
      </ol>
      {next ? (
        <span className="text-[13px] text-muted" data-testid="percorso-prossima">{t("percorso.next", { name: t(`percorso.tappa.${next.key}.title` as Key) })}</span>
      ) : (
        <span className="text-[13px] text-muted">{t("percorso.allDone")}</span>
      )}
    </div>
  );
}

function statusLabel(tp: Tappa, t: ReturnType<typeof useT>) {
  if (tp.not_needed) return t("percorso.stato.off");
  if (tp.optional && tp.status !== "done") return t("percorso.stato.optional");
  return t(`percorso.stato.${tp.status}` as Key);
}

function TappaRow({ tappa: tp, index, last }: { tappa: Tappa; index: number; last: boolean }) {
  const t = useT();
  const link = TAPPA_LINK[tp.key];
  const title = t(`percorso.tappa.${tp.key}.title` as Key);
  const issues = tp.not_needed ? [] : tp.issues;
  return (
    <li id={`tappa-${tp.key}`} className="flex gap-3" data-testid={`percorso-tappa-${tp.key}`} data-status={tp.not_needed ? "off" : tp.status}>
      {/* La rotaia del sentiero: il nodo e il filo che porta alla tappa dopo. */}
      <div className="flex flex-col items-center">
        <span aria-hidden="true" className={`inline-flex size-7 shrink-0 items-center justify-center border font-mono text-[12.5px] ${STATUS_CLASS[tp.status]}`}>
          {tp.status === "done" ? "✓" : index + 1}
        </span>
        {!last && <span aria-hidden="true" className={`w-0.5 flex-1 ${tp.status === "done" ? "bg-ok" : "bg-hair"}`} />}
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-1.5 pb-6">
        <div className="flex flex-wrap items-baseline gap-x-3">
          <h2 className="m-0 text-[18px] font-bold">{title}</h2>
          <span className={`font-mono text-[11.5px] ${tp.status === "partial" ? "text-flag" : tp.status === "done" ? "text-ok" : "text-muted"}`} data-testid={`percorso-tappa-${tp.key}-stato`}>
            {statusLabel(tp, t)}
          </span>
        </div>
        <p className="m-0 text-[13.5px] text-muted">{t(`percorso.tappa.${tp.key}.text` as Key)}</p>
        <p className="m-0 text-[13px] tabular" data-testid={`percorso-tappa-${tp.key}-conteggi`}>
          {t(`percorso.tappa.${tp.key}.counts` as Key, tp.counts as never)}
        </p>
        {tp.modules && (
          <ul className="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-[13px]">
            {tp.modules.map((m) => (
              <li key={m.key}>
                {m.name}: <span className={m.enabled ? "text-ok" : "text-muted"}>{m.enabled ? t("percorso.moduloAcceso") : t("percorso.moduloSpento")}</span>
              </li>
            ))}
          </ul>
        )}
        {issues.length > 0 && (
          <ul className="m-0 flex list-none flex-col gap-1 p-0" data-testid={`percorso-tappa-${tp.key}-incoerenze`}>
            {issues.map((is) => {
              const to = ISSUE_LINK[is.code];
              return (
                <li key={is.code} className="flex flex-wrap items-center gap-x-3 text-[13px]">
                  <span className="text-flag" aria-hidden="true">●</span>
                  <span data-testid={`percorso-incoerenza-${is.code}`}>{t(`percorso.issue.${is.code}` as Key, { count: is.count })}</span>
                  <Link to={to.to as never} search={to.search as never} className="touch-44 inline-flex items-center font-semibold text-accent underline">
                    {t("percorso.sistema")}
                  </Link>
                </li>
              );
            })}
          </ul>
        )}
        <div className="flex flex-wrap items-center gap-2 pt-1">
          <Link
            to={link.to as never}
            search={link.search as never}
            className={`btn no-underline ${tp.status !== "done" && !tp.optional && !tp.not_needed ? "btn-primary" : ""}`}
            aria-label={t("percorso.apri", { action: tp.status === "done" ? t("percorso.rivedi") : t("percorso.vai"), name: title })}
          >
            {tp.status === "done" ? t("percorso.rivedi") : t("percorso.vai")}
          </Link>
          {HAS_MODEL.includes(tp.key) && (
            <Link to="/configurazione/avvio" search={{ tab: "modelli" }} className="btn no-underline">
              {t("percorso.modello")}
            </Link>
          )}
          {tp.key === "processi" && (
            <Link to="/mappa-processi" className="btn btn-quiet no-underline">
              {t("percorso.mappa")}
            </Link>
          )}
        </div>
      </div>
    </li>
  );
}
