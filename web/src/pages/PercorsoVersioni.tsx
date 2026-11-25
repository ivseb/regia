// Nel percorso della richiesta: la guida della fase attuale e del processo, e l'avviso sobrio quando la
// richiesta è ancora su una versione del processo sostituita da una più nuova (docs/sviluppo/versioni-vigore.md).
import { Link } from "@tanstack/react-router";
import { date } from "../ui";
import { useT } from "../i18n/react";
import type { PcGuide, Percorso } from "../percorsoApi";
import { guideHref } from "./ProcessGuides";

function GuideLink({ g }: { g: PcGuide }) {
  const t = useT();
  const kind = t(`configurazione.versioni.guides.kind.${g.kind}`);
  const cls = "min-w-0 break-words text-accent no-underline hover:underline";
  return (
    <li className="flex min-w-0 flex-wrap items-baseline gap-x-2">
      <span className="text-[11.5px] text-muted">{kind}</span>
      {g.kind === "knowledge" ? (
        <Link to="/documenti/$key" params={{ key: g.target }} className={cls}>
          {g.title}
        </Link>
      ) : (
        <a href={guideHref(g)} className={cls} target="_blank" rel="noopener noreferrer">
          {g.title}
        </a>
      )}
    </li>
  );
}

/** «Guida di questa fase»: le guide della fase attuale, poi quelle di tutto il processo. */
export function PercorsoGuide({ p, phaseName }: { p: Percorso; phaseName: string }) {
  const t = useT();
  const inPhase = p.guides.filter((g) => g.phase !== null);
  const whole = p.guides.filter((g) => g.phase === null);
  if (!p.guides.length) return null;
  return (
    <div className="flex min-w-0 flex-col gap-2">
      {inPhase.length > 0 && (
        <section aria-label={t("configurazione.versioni.percorso.guidePhase")} className="flex flex-col gap-1">
          <h3 className="label m-0">{t("configurazione.versioni.percorso.guidePhaseNamed", { phase: phaseName })}</h3>
          <ul className="m-0 flex list-none flex-col gap-1 p-0">
            {inPhase.map((g) => (
              <GuideLink key={g.id} g={g} />
            ))}
          </ul>
        </section>
      )}
      {whole.length > 0 && (
        <section aria-label={t("configurazione.versioni.percorso.guideProcess")} className="flex flex-col gap-1">
          <h3 className="label m-0">{t("configurazione.versioni.percorso.guideProcess")}</h3>
          <ul className="m-0 flex list-none flex-col gap-1 p-0">
            {whole.map((g) => (
              <GuideLink key={g.id} g={g} />
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

/** Avviso di versione deprecata: c'è un nuovo standard dal …, adegua. Una riga, nessun allarme. */
export function VersionNotice({ p }: { p: Percorso }) {
  const t = useT();
  const n = p.version_notice;
  if (!n) return null;
  return (
    <div role="note" className="flex min-w-0 flex-col gap-1 border-l-2 border-flag pl-3 text-[13px]">
      <span className="font-semibold">{t("configurazione.versioni.percorso.noticeTitle", { date: date(n.since) })}</span>
      <span className="text-muted">
        {t("configurazione.versioni.percorso.noticeBody", { process: n.process_name, version: n.version, current: n.current_version })}
        {n.ends_at ? ` ${t("configurazione.versioni.percorso.noticeEnds", { date: date(n.ends_at) })}` : ""}
        {!n.phase_exists ? ` ${t("configurazione.versioni.percorso.noticePhase")}` : ""}
      </span>
      {n.can_migrate ? (
        <Link to="/processi/$key" params={{ key: n.process_key }} hash="migrazione" className="w-fit text-accent no-underline hover:underline">
          {t("configurazione.versioni.percorso.noticeMigrate")}
        </Link>
      ) : (
        <span className="text-muted">{t("configurazione.versioni.percorso.noticeAsk")}</span>
      )}
    </div>
  );
}
