import { EntitySelect, Select } from "../select";
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type ElementDetail, type Impact } from "../api";
import { CHANGE_KINDS, Criticality, ErrorNote, SectionTitle } from "../ui";
import { useT } from "../i18n/react";

// Analisi tecnica della richiesta (sdlc.md §4): asset citati, applicazioni proposte
// dalla composizione, impatti per applicazione con il team owner.

export function TechnicalAnalysis({ detail, onDone }: { detail: ElementDetail; onDone: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const key = detail.element.key;
  const an = useQuery({ queryKey: ["analysis", key], queryFn: () => api.analysis(key) });
  const [citing, setCiting] = useState(false);
  const [adding, setAdding] = useState(false);
  const assets = useQuery({ queryKey: ["assets"], queryFn: api.assets, enabled: citing });
  const [appId, setAppId] = useState("");
  const [kind, setKind] = useState("change");
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["analysis", key] });
    qc.invalidateQueries({ queryKey: ["activity"] });
    onDone();
  };
  const cite = useMutation({ mutationFn: (id: string) => api.citeAsset(key, id), onSuccess: refresh });
  const uncite = useMutation({ mutationFn: (id: string) => api.unciteAsset(key, id), onSuccess: refresh });
  const createImpact = useMutation({
    mutationFn: (application_id: string) => api.createImpact(key, { application_id, change_kind: kind }),
    onSuccess: () => {
      setAppId("");
      setAdding(false);
      refresh();
    },
  });
  if (!an.data) return <ErrorNote error={an.error} />;
  const a = an.data;
  const cited = new Set(a.assets.map((x) => x.id));
  const impacted = new Set(a.impacts.map((x) => x.application_id));
  const totalDays = a.impacts.reduce((n, i) => n + (i.state_category === "cancelled" ? 0 : i.estimate_effort_days ?? 0), 0);
  const missing = a.impacts.filter((i) => i.estimate_effort_days == null && i.state_category !== "cancelled" && i.state_category !== "done").length;
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle
        aside={
          a.impacts.length > 0 && (
            <span className="text-[12px] text-muted tabular">
              {t("lavoro.analysis.estimatedDays", { n: totalDays })}
              {missing > 0 && <span className="text-flag"> · {t("lavoro.analysis.toEstimate", { n: missing })}</span>}
            </span>
          )
        }
      >
        {t("lavoro.analysis.title")}
      </SectionTitle>
      {a.impacts.length > 0 && (
        <Link
          to="/richieste/$key/piano"
          params={{ key }}
          className="flex items-center justify-between border border-rule px-3 py-2 text-fg no-underline hover:border-accent hover:text-accent"
        >
          <span>
            <span className="font-semibold">{t("lavoro.analysis.planGantt")}</span>{" "}
            <span className="text-[12.5px] text-muted">
              {t("lavoro.analysis.planHint", { n: a.impacts.filter((i) => i.state_category !== "cancelled").length })}
            </span>
          </span>
          <span aria-hidden="true">→</span>
        </Link>
      )}

      <div className="flex flex-wrap items-center gap-2">
        <span className="label mr-1">{t("lavoro.analysis.assets")}</span>
        {a.assets.length === 0 && <span className="text-[12.5px] text-muted">{t("lavoro.analysis.noAssets")}</span>}
        {a.assets.map((x) => (
          <span key={x.id} className="inline-flex items-center gap-1.5 border border-rule px-2 py-0.5 text-[12.5px]">
            {x.name}
            <button aria-label={t("lavoro.analysis.remove", { name: x.name })} className="text-muted hover:text-danger" onClick={() => uncite.mutate(x.id)}>
              ×
            </button>
          </span>
        ))}
        {!citing ? (
          <button className="text-[12px] text-accent underline" onClick={() => setCiting(true)}>
            {t("lavoro.analysis.cite")}
          </button>
        ) : (
          <Select
            aria-label={t("lavoro.analysis.citeLabel")}
            className="field w-56 py-1"
            value=""
            onChange={(e) => {
              if (e.target.value) cite.mutate(e.target.value);
              setCiting(false);
            }}
          >
            <option value="">{t("lavoro.analysis.choose")}</option>
            {assets.data?.items
              .filter((x) => !cited.has(x.id))
              .map((x) => (
                <option key={x.id} value={x.id}>
                  {x.name}
                </option>
              ))}
          </Select>
        )}
      </div>

      {a.can_edit && a.proposed.length > 0 && (
        <div className="flex flex-col gap-1 border-l-2 border-accent pl-3">
          <span className="text-[12.5px] text-muted">{t("lavoro.analysis.proposedHead")}</span>
          {a.proposed.map((p) => (
            <div key={p.application_id} className="flex flex-wrap items-center gap-2 text-[12.5px]">
              <span className="font-medium">{p.name}</span>
              <span className="text-muted">
                {t(p.relation === "uses" ? "lavoro.analysis.usedBy" : "lavoro.analysis.partOf")} {p.via} · {p.team ?? t("lavoro.analysis.noTeam")}
              </span>
              <button className="text-accent underline" disabled={createImpact.isPending} onClick={() => createImpact.mutate(p.application_id)}>
                {t("lavoro.analysis.openImpactLower")}
              </button>
            </div>
          ))}
        </div>
      )}

      {a.impacts.length > 0 && (
        <table className="w-full border-collapse text-[12.5px]">
          <thead>
            <tr className="label h-8 text-left">
              <th scope="col" className="border-b border-rule pr-2">{t("lavoro.analysis.colImpact")}</th>
              <th scope="col" className="border-b border-rule px-2">{t("lavoro.analysis.colApplication")}</th>
              <th scope="col" className="border-b border-rule px-2">{t("lavoro.analysis.colTeam")}</th>
              <th scope="col" className="border-b border-rule px-2 text-right">{t("lavoro.analysis.colDays")}</th>
              <th scope="col" className="border-b border-rule pl-2">{t("lavoro.analysis.colStage")}</th>
            </tr>
          </thead>
          <tbody>
            {a.impacts.map((i) => (
              <tr key={i.id} className={`border-b border-hair ${i.state_category === "cancelled" ? "text-muted line-through" : ""}`}>
                <td className="py-1.5 pr-2 font-mono text-[12px]">
                  <Link to="/e/$key" params={{ key: i.key }} className="text-accent no-underline hover:underline">
                    {i.key}
                  </Link>
                </td>
                <td className="px-2 py-1.5">
                  <span className="font-medium">{i.application}</span>{" "}
                  <span className="text-muted">· {CHANGE_KINDS[i.change_kind] ?? i.change_kind}</span>
                  {i.depends_on.length > 0 && <span className="block text-[11.5px] text-muted">{t("lavoro.analysis.after", { list: i.depends_on.join(", ") })}</span>}
                </td>
                <td className="px-2 py-1.5">{i.team ?? <span className="text-flag">{t("lavoro.analysis.none")}</span>}</td>
                <td className="px-2 py-1.5 text-right font-mono tabular">{i.estimate_effort_days ?? <span className="text-flag">—</span>}</td>
                <td className="py-1.5 pl-2 whitespace-nowrap">{i.state}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {a.can_edit &&
        (!adding ? (
          <button className="self-start text-[12px] text-accent underline" onClick={() => setAdding(true)}>
            {t("lavoro.analysis.addImpact")}
          </button>
        ) : (
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              createImpact.mutate(appId);
            }}
          >
            <EntitySelect kind="application" aria-label={t("lavoro.analysis.colApplication")} className="field min-w-0 flex-[1_1_200px]" required noneLabel={t("lavoro.analysis.applicationNone")} filter={(o) => !impacted.has(o.value)} value={appId} onChange={setAppId} />
            <Select aria-label={t("lavoro.analysis.interventionType")} className="field w-44" value={kind} onChange={(e) => setKind(e.target.value)}>
              {Object.entries(CHANGE_KINDS).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </Select>
            <button className="btn btn-primary" disabled={createImpact.isPending}>{t("lavoro.analysis.openImpact")}</button>
            <button type="button" className="btn" onClick={() => setAdding(false)}>{t("lavoro.analysis.cancel")}</button>
          </form>
        ))}
      <ErrorNote error={cite.error ?? uncite.error ?? createImpact.error} />
    </section>
  );
}

/** Dati dell'impatto nel suo dettaglio: richiesta, applicazione, team, stima, nota tecnica. */
export function ImpactFacts({ impact, onDone }: { impact: Impact; onDone: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [days, setDays] = useState(impact.estimate_effort_days?.toString() ?? "");
  const [note, setNote] = useState(impact.technical_note);
  const [kind, setKind] = useState(impact.change_kind);
  const [deps, setDeps] = useState(impact.depends_on.join(", "));
  const save = useMutation({
    mutationFn: () =>
      api.updateImpact(impact.id, {
        estimate_effort_days: days === "" ? undefined : Number(days),
        technical_note: note,
        change_kind: kind,
        depends_on: deps
          .split(/[\s,]+/)
          .map((d) => d.trim())
          .filter(Boolean),
      }),
    onSuccess: () => {
      setEditing(false);
      qc.invalidateQueries({ queryKey: ["analysis"] });
      onDone();
    },
  });
  const rows: [string, React.ReactNode][] = [
    [
      t("lavoro.analysis.request"),
      <Link to="/e/$key" params={{ key: impact.request_key }} className="text-accent no-underline hover:underline">
        {impact.request_key} · {impact.request_title}
      </Link>,
    ],
    [t("lavoro.analysis.colApplication"), impact.application],
    [t("lavoro.analysis.criticality"), <Criticality value={impact.criticality} critical={impact.supports_critical_function} />],
    [t("lavoro.analysis.teamOwner"), impact.team ?? "—"],
    [t("lavoro.analysis.intervention"), CHANGE_KINDS[impact.change_kind] ?? impact.change_kind],
    [t("lavoro.analysis.estimate"), impact.estimate_effort_days != null ? t("lavoro.analysis.personDays", { n: impact.estimate_effort_days }) : "—"],
    [t("lavoro.analysis.dependsOn"), impact.depends_on.join(", ") || "—"],
  ];
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle aside={!editing && <button className="text-[12px] text-accent underline" onClick={() => setEditing(true)}>{t("lavoro.analysis.update")}</button>}>
        {t("lavoro.analysis.colImpact")}
      </SectionTitle>
      {!editing ? (
        <>
          <dl className="m-0 grid grid-cols-[120px_minmax(0,1fr)]">
            {rows.map(([k, v]) => (
              <div key={k} className="contents">
                <dt className="border-b border-hair py-1.5 text-muted">{k}</dt>
                <dd className="m-0 border-b border-hair py-1.5">{v}</dd>
              </div>
            ))}
          </dl>
          {impact.technical_note && <p className="m-0 leading-relaxed whitespace-pre-line">{impact.technical_note}</p>}
        </>
      ) : (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <div className="flex flex-wrap gap-2">
            <label className="flex w-32 flex-col gap-1">
              <span className="label">{t("lavoro.analysis.daysPerPerson")}</span>
              <input className="field" inputMode="decimal" value={days} onChange={(e) => setDays(e.target.value)} />
            </label>
            <label className="flex w-48 flex-col gap-1">
              <span className="label">{t("lavoro.analysis.intervention")}</span>
              <Select className="field" value={kind} onChange={(e) => setKind(e.target.value)}>
                {Object.entries(CHANGE_KINDS).map(([k, v]) => (
                  <option key={k} value={k}>
                    {v}
                  </option>
                ))}
              </Select>
            </label>
            <label className="flex min-w-0 flex-[1_1_160px] flex-col gap-1">
              <span className="label">{t("lavoro.analysis.dependsOn")}</span>
              <input className="field font-mono" value={deps} onChange={(e) => setDeps(e.target.value)} placeholder="DEM-12, DEM-13" />
            </label>
          </div>
          <label className="flex flex-col gap-1">
            <span className="label">{t("lavoro.analysis.title")}</span>
            <textarea className="field min-h-24" value={note} onChange={(e) => setNote(e.target.value)} />
          </label>
          <div className="flex gap-2">
            <button className="btn btn-primary" disabled={save.isPending}>{t("lavoro.analysis.save")}</button>
            <button type="button" className="btn" onClick={() => setEditing(false)}>{t("lavoro.analysis.cancel")}</button>
          </div>
          <ErrorNote error={save.error} />
        </form>
      )}
    </section>
  );
}
