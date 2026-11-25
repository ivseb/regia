// Configurazione → Inizia da qui (replica/usabilita.md, proposta 7B): la checklist del primo avvio con
// stato calcolato dal sistema, i modelli per tipo di azienda con anteprima prima di applicare e
// l'elenco di ciò che è applicato, con il ripristino di un processo di partenza.
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api";
import { avvioApi, MODEL_KEYS, type Applied as AppliedData, type AppliedProcess, type AvvioItem, type AvvioPreview, type ItemKind, type ModelKey, type StepKey } from "../avvioApi";
import { diffList, type Change } from "../casa/differenze";
import type { Definition } from "../api";
import { PageHeader } from "../shell";
import { ErrorNote, Loading, SectionTitle, date } from "../ui";
import { useT } from "../i18n/react";
import type { Key } from "../i18n/core";

export type AvvioTab = "passi" | "modelli" | "applicato";
const TABS: AvvioTab[] = ["passi", "modelli", "applicato"];

/** Dove porta ogni passo. */
const STEP_LINK: Record<StepKey, { to: string; search?: Record<string, string> }> = {
  organizzazione: { to: "/profilo" },
  moduli: { to: "/configurazione/moduli" },
  persone: { to: "/configurazione/persone" },
  team: { to: "/architettura", search: { tab: "team" } },
  processo: { to: "/processi" },
  organi: { to: "/governance" },
  norme: { to: "/conformita", search: { tab: "cataloghi" } },
  richiesta: { to: "/richieste/nuova" },
};

export function AvvioPage({ tab }: { tab: AvvioTab }) {
  const t = useT();
  return (
    <>
      <PageHeader path={t("configurazione.avvio.path")} title={t("configurazione.avvio.title")} />
      <div className="flex max-w-[860px] flex-col gap-5 px-4 pb-12 md:px-7">
        <div role="tablist" aria-label={t("configurazione.avvio.title")} className="flex flex-wrap gap-x-5 border-b border-hair">
          {TABS.map((k) => (
            <Link
              key={k}
              to="/configurazione/avvio"
              search={{ tab: k }}
              role="tab"
              aria-selected={tab === k}
              className={`inline-flex min-h-11 items-center no-underline ${tab === k ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
            >
              {t(`configurazione.avvio.tab.${k}` as Key)}
            </Link>
          ))}
        </div>
        {tab === "passi" && <Steps />}
        {tab === "modelli" && <Models />}
        {tab === "applicato" && <AppliedTab />}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------
// Passi

function Steps() {
  const t = useT();
  const qc = useQueryClient();
  const state = useQuery({ queryKey: ["avvio"], queryFn: avvioApi.state });
  const toggle = useMutation({
    mutationFn: (close: boolean) => (close ? avvioApi.close() : avvioApi.reopen()),
    onSuccess: (data) => qc.setQueryData(["avvio"], data),
  });
  if (state.isPending) return <Loading />;
  if (!state.data) return <ErrorNote error={state.error} />;
  const s = state.data;
  const complete = s.done === s.total;
  return (
    <div className="flex flex-col gap-4">
      <p className="m-0 max-w-3xl text-muted">{t("configurazione.avvio.intro")}</p>
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-semibold tabular" data-testid="avvio-progress">{t("configurazione.avvio.progress", { done: s.done, total: s.total })}</span>
        <div className="h-2 min-w-32 flex-1 bg-sel" aria-hidden="true">
          <div className="h-2 bg-ok" style={{ width: `${(s.done / s.total) * 100}%` }} />
        </div>
      </div>
      <ol className="m-0 flex list-none flex-col p-0">
        {s.steps.map((st, i) => {
          const link = STEP_LINK[st.key];
          return (
            <li key={st.key} className="flex flex-wrap items-start gap-x-3 gap-y-2 border-b border-hair py-3" data-testid={`passo-${st.key}`} data-done={st.done ? "true" : "false"}>
              <span
                aria-hidden="true"
                className={`mt-0.5 inline-flex size-7 shrink-0 items-center justify-center border font-mono text-[12.5px] ${st.done ? "border-ok bg-ok text-bg" : "border-rule text-muted"}`}
              >
                {st.done ? "✓" : i + 1}
              </span>
              <div className="flex min-w-0 flex-1 basis-56 flex-col gap-0.5">
                <span className="font-semibold">
                  {t(`configurazione.avvio.step.${st.key}.title` as Key)}
                  <span className="sr-only"> — {st.done ? t("configurazione.avvio.stato.fatto") : t("configurazione.avvio.stato.dafare")}</span>
                </span>
                <span className="text-[13px] text-muted">{t(`configurazione.avvio.step.${st.key}.text` as Key)}</span>
                <span className="text-[12.5px] text-muted" data-testid={`passo-${st.key}-stato`}>
                  {st.not_needed ? t("configurazione.avvio.nonServe") : t(`configurazione.avvio.step.${st.key}.${st.done ? "done" : "todo"}` as Key, { count: Math.max(0, st.count) })}
                </span>
              </div>
              <Link to={link.to as never} search={link.search as never} className="btn no-underline" aria-label={t("configurazione.avvio.apri", { name: t(`configurazione.avvio.step.${st.key}.title` as Key) })}>
                {st.done ? t("configurazione.avvio.rivedi") : t("configurazione.avvio.vai")}
              </Link>
            </li>
          );
        })}
      </ol>
      <div className="flex flex-wrap items-center gap-3">
        <Link to="/configurazione/avvio" search={{ tab: "modelli" }} className="btn no-underline">
          {t("configurazione.avvio.vediModelli")}
        </Link>
        <Link to="/configurazione/percorso" className="btn no-underline" data-testid="avvio-vai-percorso">
          {t("percorso.vediPercorso")}
        </Link>
        {complete && !s.closed && (
          <button className="btn btn-primary" disabled={toggle.isPending} onClick={() => toggle.mutate(true)}>
            {t("configurazione.avvio.richiudi")}
          </button>
        )}
        {s.closed && (
          <>
            <span className="text-muted" role="status">{t("configurazione.avvio.richiusa")}</span>
            <button className="btn" disabled={toggle.isPending} onClick={() => toggle.mutate(false)}>
              {t("configurazione.avvio.riapri")}
            </button>
          </>
        )}
        {!complete && <span className="text-[12.5px] text-muted">{t("configurazione.avvio.richiudiDopo")}</span>}
      </div>
      <ErrorNote error={toggle.error} />
    </div>
  );
}

// ---------------------------------------------------------------------
// Modelli

const KIND_ORDER: ItemKind[] = ["module", "type", "process", "body", "catalog"];

function Models() {
  const t = useT();
  const qc = useQueryClient();
  const models = useQuery({ queryKey: ["avvio", "modelli"], queryFn: avvioApi.models });
  const [open, setOpen] = useState<ModelKey | null>(null);
  const [result, setResult] = useState<AvvioPreview | null>(null);
  const preview = useQuery({ queryKey: ["avvio", "anteprima", open], queryFn: () => avvioApi.preview(open!), enabled: !!open && !result });
  const apply = useMutation({
    mutationFn: (key: ModelKey) => avvioApi.apply(key),
    onSuccess: (data) => {
      setResult(data);
      qc.invalidateQueries({ queryKey: ["avvio"] });
      qc.invalidateQueries({ queryKey: ["me"] });
      qc.invalidateQueries({ queryKey: ["tenant-modules"] });
    },
  });
  const rows = new Map((models.data?.items ?? []).map((m) => [m.key, m]));
  return (
    <div className="flex flex-col gap-4">
      <p className="m-0 max-w-3xl text-muted">{t("configurazione.avvio.modelli.intro")}</p>
      {models.isPending && <Loading />}
      <ErrorNote error={models.error} />
      <ul className="m-0 flex list-none flex-col p-0">
        {MODEL_KEYS.map((k) => {
          const row = rows.get(k);
          const available = !!row?.available;
          return (
            <li key={k} className="flex flex-wrap items-start gap-x-4 gap-y-2 border-b border-hair py-3" data-testid={`modello-${k}`}>
              <div className="flex min-w-0 flex-1 basis-64 flex-col gap-1">
                <span className="font-semibold">
                  {t(`configurazione.avvio.modello.${k}.nome` as Key)}
                  {!available && <span className="ml-2 border border-rule px-1.5 font-mono text-[11.5px] font-normal text-muted">{t("configurazione.avvio.inArrivo")}</span>}
                  {row?.applied && <span className="ml-2 border border-ok px-1.5 font-mono text-[11.5px] font-normal text-ok">{t("configurazione.avvio.applicato")}</span>}
                </span>
                <span className="text-[13px] text-muted">{t(`configurazione.avvio.modello.${k}.testo` as Key)}</span>
              </div>
              {available && (
                <button
                  className="btn"
                  aria-expanded={open === k}
                  onClick={() => {
                    setResult(null);
                    setOpen(open === k ? null : k);
                  }}
                >
                  {open === k ? t("configurazione.avvio.chiudiAnteprima") : t("configurazione.avvio.anteprima")}
                </button>
              )}
              {open === k && (
                <div className="flex w-full basis-full flex-col gap-3 border-t border-hair pt-3">
                  {!result && preview.isPending && <Loading />}
                  <ErrorNote error={preview.error} />
                  {!result && preview.data && <PreviewList data={preview.data} />}
                  {result && <PreviewList data={result} done />}
                  <ErrorNote error={apply.error} />
                  {!result && preview.data && (
                    <div className="flex flex-wrap items-center gap-3">
                      <button className="btn btn-primary" disabled={apply.isPending} onClick={() => apply.mutate(k)}>
                        {apply.isPending ? t("configurazione.avvio.applicazione") : t("configurazione.avvio.applica")}
                      </button>
                      <span className="text-[12.5px] text-muted">{t("configurazione.avvio.nonCancellaNiente")}</span>
                    </div>
                  )}
                  {result && (
                    <div className="flex flex-wrap items-center gap-3">
                      <Link to="/configurazione/avvio" search={{ tab: "passi" }} className="btn btn-primary no-underline">{t("configurazione.avvio.tornaAiPassi")}</Link>
                      <Link to="/configurazione/avvio" search={{ tab: "applicato" }} className="btn no-underline">{t("configurazione.avvio.vediApplicato")}</Link>
                    </div>
                  )}
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function PreviewList({ data, done = false }: { data: AvvioPreview; done?: boolean }) {
  const t = useT();
  const unverified = data.items.some((i) => i.unverified);
  return (
    <div className="flex flex-col gap-3" data-testid={done ? "modello-applicato" : "modello-anteprima"}>
      <p className="m-0 font-semibold" role="status">
        {done ? t("configurazione.avvio.fatto", { new: data.new, present: data.present }) : t("configurazione.avvio.creera", { new: data.new, present: data.present })}
      </p>
      {unverified && (
        <p role="note" className="m-0 border border-danger p-3 text-[13px]" data-testid="nota-da-verificare">
          <span className="font-semibold">{t("configurazione.avvio.daVerificare")}. </span>
          {t("configurazione.avvio.notaNormativa")}
        </p>
      )}
      {KIND_ORDER.map((kind) => {
        const items = data.items.filter((i) => i.kind === kind);
        if (items.length === 0) return null;
        return (
          <section key={kind} aria-labelledby={`k-${kind}`} className="flex flex-col">
            <h3 id={`k-${kind}`} className="label m-0 border-b-2 border-rule pb-1">
              {t(`configurazione.avvio.kind.${kind}` as Key)} <span className="font-mono tabular">{items.length}</span>
            </h3>
            <ul className="m-0 list-none p-0">
              {items.map((it) => (
                <PreviewRow key={`${it.kind}:${it.key}`} item={it} done={done} />
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}

function PreviewRow({ item, done }: { item: AvvioItem; done: boolean }) {
  const t = useT();
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-hair py-2" data-state={item.state}>
      <span className="min-w-0 flex-1 basis-48">{item.name}</span>
      {item.detail ? <span className="font-mono text-[12px] text-muted tabular">{t("configurazione.avvio.voci", { count: item.detail })}</span> : null}
      {item.unverified && <span className="border border-danger px-1.5 font-mono text-[11.5px] text-danger">{t("configurazione.avvio.daVerificare")}</span>}
      <span className={`font-mono text-[11.5px] ${item.state === "new" ? "font-semibold text-fg" : "text-muted"}`}>
        {item.state === "new" ? (done ? t("configurazione.avvio.stato.creato") : t("configurazione.avvio.stato.nuovo")) : t("configurazione.avvio.stato.presente")}
      </span>
    </li>
  );
}

// ---------------------------------------------------------------------
// Cosa è applicato

function AppliedTab() {
  const t = useT();
  const qc = useQueryClient();
  const applied = useQuery({ queryKey: ["avvio", "applicato"], queryFn: avvioApi.applied });
  const [restoring, setRestoring] = useState<string | null>(null);
  const verify = useMutation({
    mutationFn: (key: string) => avvioApi.verify(key),
    onSuccess: (data) => qc.setQueryData(["avvio", "applicato"], data),
  });
  if (applied.isPending) return <Loading />;
  if (!applied.data) return <ErrorNote error={applied.error} />;
  const a = applied.data;
  return (
    <div className="flex flex-col gap-6">
      <p className="m-0 max-w-3xl text-muted">{t("configurazione.avvio.applicato.intro")}</p>

      <section className="flex flex-col" aria-label={t("configurazione.avvio.applicato.modelli")}>
        <SectionTitle>{t("configurazione.avvio.applicato.modelli")}</SectionTitle>
        {a.models.length === 0 && <p className="m-0 text-muted">{t("configurazione.avvio.applicato.nessunModello")}</p>}
        {a.models.map((m) => (
          <div key={m.key} className="border-b border-hair py-2">
            <span className="font-semibold">{t(`configurazione.avvio.modello.${m.key}.nome` as Key)}</span>
            <span className="text-muted"> — {t("configurazione.avvio.applicato.il", { date: date(m.applied_at), by: m.applied_by ?? "—" })}</span>
          </div>
        ))}
      </section>

      <section className="flex flex-col" aria-label={t("configurazione.avvio.applicato.processi")}>
        <SectionTitle>{t("configurazione.avvio.applicato.processi")}</SectionTitle>
        {a.processes.map((p) => (
          <div key={p.key} className="flex flex-col gap-2 border-b border-hair py-2.5" data-testid={`processo-${p.key}`} data-state={p.state}>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span className="min-w-0 flex-1 basis-56">
                <span className="font-semibold">{p.name}</span>
                <span className="text-[12.5px] text-muted"> · {p.type_name} · {t("configurazione.avvio.versione", { n: p.version })}</span>
              </span>
              <span className={`font-mono text-[11.5px] ${p.state === "modified" ? "font-semibold text-danger" : "text-muted"}`}>
                {t(`configurazione.avvio.processo.${p.state}` as Key)}
              </span>
              {p.restored_at && <span className="text-[12px] text-muted">{t("configurazione.avvio.ripristinatoIl", { date: date(p.restored_at) })}</span>}
              {p.state === "modified" && restoring !== p.key && (
                <button className="btn" onClick={() => setRestoring(p.key)} aria-label={t("configurazione.avvio.ripristinaPacchetto", { name: p.name })}>
                  {t("configurazione.avvio.ripristina")}
                </button>
              )}
            </div>
            {restoring === p.key && <RestorePanel p={p} onClose={() => setRestoring(null)} onDone={(data) => qc.setQueryData(["avvio", "applicato"], data)} />}
          </div>
        ))}
      </section>

      <section className="flex flex-col" aria-label={t("configurazione.avvio.applicato.cataloghi")}>
        <SectionTitle>{t("configurazione.avvio.applicato.cataloghi")}</SectionTitle>
        {a.catalogs.length === 0 && <p className="m-0 text-muted">{t("configurazione.avvio.applicato.nessunCatalogo")}</p>}
        {a.catalogs.map((c) => (
          <div key={c.key} className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-hair py-2.5" data-testid={`catalogo-${c.key}`} data-unverified={c.unverified ? "true" : "false"}>
            <span className="min-w-0 flex-1 basis-56 font-semibold">{c.name}</span>
            {c.unverified ? (
              <>
                <span className="border border-danger px-1.5 font-mono text-[11.5px] text-danger">{t("configurazione.avvio.daVerificare")}</span>
                <button className="btn" disabled={verify.isPending} onClick={() => verify.mutate(c.key)} aria-label={t("configurazione.avvio.segnaVerificato", { name: c.name })}>
                  {t("configurazione.avvio.segnaVerificatoBreve")}
                </button>
              </>
            ) : (
              <span className="text-[12.5px] text-ok">{t("configurazione.avvio.verificatoIl", { date: date(c.verified_at), by: c.verified_by ?? "—" })}</span>
            )}
          </div>
        ))}
        {a.catalogs.some((c) => c.unverified) && <p role="note" className="mt-2 mb-0 text-[12.5px] text-muted">{t("configurazione.avvio.notaNormativa")}</p>}
        <ErrorNote error={verify.error} />
      </section>

      <section className="flex flex-col" aria-label={t("configurazione.avvio.applicato.organi")}>
        <SectionTitle>{t("configurazione.avvio.applicato.organi")}</SectionTitle>
        {a.bodies.length === 0 && <p className="m-0 text-muted">{t("configurazione.avvio.applicato.nessunOrgano")}</p>}
        {a.bodies.map((b) => (
          <div key={b.key} className="border-b border-hair py-2">{b.name}</div>
        ))}
      </section>

      <section className="flex flex-col" aria-label={t("configurazione.avvio.kind.module")}>
        <SectionTitle>{t("configurazione.avvio.kind.module")}</SectionTitle>
        {a.modules.map((m) => (
          <div key={m.key} className="flex gap-3 border-b border-hair py-2">
            <span className="flex-1">{m.name}</span>
            <span className={`font-mono text-[11.5px] ${m.enabled ? "text-ok" : "text-muted"}`}>{m.enabled ? t("configurazione.modules.on") : t("configurazione.modules.off")}</span>
          </div>
        ))}
        <Link to="/configurazione/moduli" className="touch-44 mt-2 inline-flex items-center text-accent underline">{t("configurazione.avvio.gestisciModuli")}</Link>
      </section>
    </div>
  );
}

function RestorePanel({ p, onClose, onDone }: { p: AppliedProcess; onClose: () => void; onDone: (a: AppliedData) => void }) {
  const t = useT();
  const prev = useQuery({ queryKey: ["avvio", "ripristino", p.key], queryFn: () => avvioApi.restorePreview(p.key) });
  const run = useMutation({
    mutationFn: (version: number) => avvioApi.restore(p.key, version),
    onSuccess: (data) => {
      onDone(data);
      onClose();
    },
  });
  const d = prev.data;
  const changes: Change[] = d && d.current ? diffDefinitions(d.current, d.original, t) : [];
  return (
    <div className="flex flex-col gap-3 border border-rule p-3" data-testid="ripristino-pannello">
      <span className="label">{t("configurazione.avvio.cosaCambia")}</span>
      {prev.isPending && <Loading />}
      <ErrorNote error={prev.error} />
      {d && (
        <>
          <p className="m-0 text-[13px]">{t("configurazione.avvio.ripristinoSpiega", { name: d.name, version: d.version })}</p>
          {changes.length === 0 && <p className="m-0 text-muted">{t("configurazione.avvio.nessunaDifferenza")}</p>}
          <ul className="m-0 flex list-none flex-col gap-2 p-0">
            {changes.map((c, i) => (
              <li key={i} className="flex flex-col gap-0.5 border-b border-hair pb-2 text-[13px]" data-testid="ripristino-differenza">
                <span className="font-semibold">{c.label}</span>
                <span className="text-muted">{t("configurazione.avvio.ora")}: {c.before}</span>
                <span>{t("configurazione.avvio.dopo")}: {c.after}</span>
              </li>
            ))}
          </ul>
          {d.has_draft && <p role="note" className="m-0 text-[12.5px] text-danger">{t("configurazione.avvio.bozzaSostituita")}</p>}
          {d.elements > 0 && <p className="m-0 text-[12.5px] text-muted">{t("configurazione.avvio.elementiRestano", { count: d.elements })}</p>}
          <ErrorNote error={run.error instanceof ApiError && run.error.status === 409 ? new ApiError(409, "conflict", t("configurazione.avvio.versioneCambiata")) : run.error} />
          <div className="flex flex-wrap gap-2">
            <button className="btn btn-primary" disabled={run.isPending || !d.modified} onClick={() => run.mutate(d.version)}>
              {run.isPending ? t("configurazione.avvio.ripristino") : t("configurazione.avvio.confermaRipristino")}
            </button>
            <button className="btn" onClick={onClose}>{t("configurazione.common.annulla")}</button>
          </div>
        </>
      )}
    </div>
  );
}

/** Differenze tra la versione in uso e quella di partenza, in parole: fasi, passaggi, gate e regole. */
function diffDefinitions(cur: Definition, orig: Definition, t: ReturnType<typeof useT>): Change[] {
  const phase = (x: Definition["phases"][number]) => `${x.category}${x.description ? ` · ${x.description}` : ""}${x.off_path ? ` · ${t("configurazione.avvio.fuoriPercorso")}` : ""}`;
  const step = (x: Definition["transitions"][number]) =>
    [x.label, (x.roles ?? []).join(", "), (x.gates ?? []).length ? `${t("configurazione.avvio.gate")}: ${(x.gates ?? []).join(", ")}` : "", x.when ?? ""].filter(Boolean).join(" · ") || "—";
  return [
    ...diffList(t("configurazione.avvio.fase"), cur.phases ?? [], orig.phases ?? [], (x) => x.name, (x) => x.name, phase),
    ...diffList(t("configurazione.avvio.passaggio"), cur.transitions ?? [], orig.transitions ?? [], (x) => `${(x.from ?? []).join("+")}>${x.to}`, (x) => `${(x.from ?? []).join(", ")} → ${x.to}`, step),
    ...diffList(t("configurazione.avvio.gate"), cur.gates ?? [], orig.gates ?? [], (x) => x.key, (x) => x.name || x.key, (x) => x.condition || x.check || x.message || "—"),
    ...diffList(t("configurazione.avvio.regola"), cur.rules ?? [], orig.rules ?? [], (x) => x.key, (x) => x.name || x.key, (x) => `${x.on}${x.when ? ` · ${x.when}` : ""} · ${(x.actions ?? []).map((a) => a.kind).join(", ")}`),
  ];
}
