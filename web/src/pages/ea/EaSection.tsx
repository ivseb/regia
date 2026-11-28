// Architettura enterprise nel dettaglio dell'elemento (applicazione, asset, capability):
// capability supportate, dipendenze nei due versi, dipendenti, TIME e tecnologie.
import { Select } from "../../select";
import { useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMe } from "../../shell";
import { ErrorNote, SectionTitle, date } from "../../ui";
import { TIME, canWriteEA, eaApi, type C4Level, type Profile, type Relation } from "../../eaApi";
import { NewDependency, attrLabel } from "./DependenciesPage";
import { useT } from "../../i18n/react";
import { Trans } from "../../i18n/Trans";
import { ContinuityBlock } from "../continuita/ContinuityBlock";

export function EaSection({ refKey }: { refKey: string }) {
  const p = useQuery({ queryKey: ["ea", "profile", refKey], queryFn: () => eaApi.profile(refKey) });
  if (p.error) return null; // l'elemento non è un nodo del grafo (o l'architettura non è visibile)
  if (!p.data) return null;
  return <ProfileView profile={p.data} />;
}

function ProfileView({ profile }: { profile: Profile }) {
  const t = useT();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canWrite = canWriteEA(perms);
  const qc = useQueryClient();
  const n = profile.node;
  const [adding, setAdding] = useState(false);
  const isApp = n.type === "application";
  const out = profile.relations.filter((r) => r.source_id === n.id);
  const inc = profile.relations.filter((r) => r.target_id === n.id);
  const schema = new Map(profile.relation_types.map((t) => [t.key, t.attrs_schema?.fields ?? []]));
  const nav = useNavigate();
  const c4 = useMutation({
    mutationFn: (level: C4Level) => eaApi.generateC4(n.id, level),
    onSuccess: (d) => {
      qc.invalidateQueries({ queryKey: ["ea", "diagrams"] });
      nav({ to: "/architettura/diagrammi/$id", params: { id: d.id } });
    },
  });
  const remove = useMutation({ mutationFn: (id: string) => eaApi.deleteRelation(id), onSuccess: () => qc.invalidateQueries({ queryKey: ["ea"] }) });
  const row = (r: Relation, other: "target" | "source") => {
    const key = other === "target" ? r.target_key : r.source_key;
    const name = other === "target" ? r.target_name : r.source_name;
    const verb = other === "target" ? r.type_name : (profile.relation_types.find((t) => t.key === r.type)?.inward_name ?? r.type_name);
    const attrs = (schema.get(r.type) ?? []).map((f) => attrLabel(f, r.attrs[f.key])).filter(Boolean);
    return (
      <li key={r.id} className="flex flex-wrap items-baseline gap-x-2 border-b border-hair py-1.5 text-[12.5px]">
        <span className="text-muted">{verb}</span>
        <Link to="/e/$key" params={{ key: key }} className="font-medium text-fg no-underline hover:underline">
          {name}
        </Link>
        {attrs.length > 0 && <span className="text-muted">· {attrs.join(" · ")}</span>}
        {canWrite && r.category === "dependency" && (
          <button className="ml-auto text-[12px] text-muted underline hover:text-danger" onClick={() => remove.mutate(r.id)}>
            {t("altro.ea.section.delete")}
          </button>
        )}
      </li>
    );
  };
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle
        aside={
          <span className="flex gap-3 text-[12px]">
            <Link to="/architettura/mappa" search={{ nodo: n.key }} className="text-accent underline">
              {t("altro.ea.section.onMap")}
            </Link>
            <a className="text-accent underline" href={eaApi.impactCsvUrl(n.key, "dependents")} download>
              {t("altro.ea.section.impactCsv")}
            </a>
          </span>
        }
      >
        {t("altro.ea.section.title")}
      </SectionTitle>
      <p className="m-0 flex flex-wrap gap-x-5 gap-y-1 text-[12.5px]">
        <span>
          <Trans k="altro.ea.section.dependents" params={{ count: profile.dependents }} slots={{ num: <span className="font-mono font-semibold">{profile.dependents}</span> }} />
          {profile.direct_dependents !== profile.dependents && <span className="text-muted"> {t("altro.ea.section.direct", { count: profile.direct_dependents })}</span>}
        </span>
        <span>
          <Trans k="altro.ea.section.dependsOn" params={{ count: profile.dependencies }} slots={{ num: <span className="font-mono font-semibold">{profile.dependencies}</span> }} />
        </span>
      </p>
      {canWrite && (isApp || n.type === "logical_asset") && (
        <div className="flex flex-wrap items-center gap-2">
          <span className="label">{t("altro.ea.section.c4")}</span>
          <button className="btn" disabled={c4.isPending} onClick={() => c4.mutate("context")}>
            {t("altro.ea.section.genContext")}
          </button>
          {isApp ? (
            <button className="btn" disabled={c4.isPending} onClick={() => c4.mutate("container")}>
              {t("altro.ea.section.genContainer")}
            </button>
          ) : (
            <button className="btn" disabled={c4.isPending} onClick={() => c4.mutate("component")}>
              {t("altro.ea.section.genComponent")}
            </button>
          )}
          <ErrorNote error={c4.error} />
        </div>
      )}
      {profile.capabilities.length > 0 && (
        <p className="m-0 text-[12.5px]">
          <span className="text-muted">{t("altro.ea.section.capabilities")}</span>
          {profile.capabilities.map((c, i) => (
            <span key={c.id}>
              {i > 0 && ", "}
              <Link to="/e/$key" params={{ key: c.key }} className="text-fg underline decoration-hair underline-offset-2 hover:text-accent" title={c.path.join(" / ")}>
                {c.name}
              </Link>
            </span>
          ))}
        </p>
      )}
      {isApp && <Lifecycle profile={profile} canWrite={canWrite} />}
      {out.length > 0 && <ul className="m-0 flex list-none flex-col p-0">{out.map((r) => row(r, "target"))}</ul>}
      {inc.length > 0 && <ul className="m-0 flex list-none flex-col p-0">{inc.map((r) => row(r, "source"))}</ul>}
      <ErrorNote error={remove.error} />
      {canWrite && isApp && !adding && (
        <button className="btn self-start" onClick={() => setAdding(true)}>
          {t("altro.ea.section.addDependency")}
        </button>
      )}
      {canWrite && isApp && adding && (
        <NewDependency types={profile.relation_types.filter((t) => t.category === "dependency")} from={n.id} onDone={() => setAdding(false)} />
      )}
      {(isApp || n.type === "capability") && <ContinuityBlock kind={isApp ? "application" : "capability"} refKey={n.key} />}
    </section>
  );
}

function Lifecycle({ profile, canWrite }: { profile: Profile; canWrite: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const n = profile.node;
  const techs = useQuery({ queryKey: ["ea", "technologies"], queryFn: eaApi.technologies, enabled: canWrite });
  const [note, setNote] = useState(profile.time_note);
  const [editTech, setEditTech] = useState(false);
  const [chosen, setChosen] = useState<string[]>(profile.technologies.map((x) => x.id));
  const done = () => qc.invalidateQueries({ queryKey: ["ea"] });
  const setTime = useMutation({ mutationFn: (v: { rating: string; note: string }) => eaApi.setTime(n.id, v.rating, v.note), onSuccess: done });
  const saveTech = useMutation({
    mutationFn: () => eaApi.setTechnologies(n.id, chosen),
    onSuccess: () => {
      setEditTech(false);
      done();
    },
  });
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex w-36 flex-col gap-1">
          {/* i18n-ok: sigla */}
          <span className="label">TIME</span>
          {canWrite ? (
            <Select className="field py-1" value={n.time ?? ""} onChange={(e) => setTime.mutate({ rating: e.target.value, note })}>
              <option value="">—</option>
              {Object.entries(TIME).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </Select>
          ) : (
            <span>{n.time ? TIME[n.time] : t("altro.ea.section.notRated")}</span>
          )}
        </label>
        <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
          <span className="label">{t("altro.ea.section.rationale")}</span>
          {canWrite ? (
            <input className="field py-1" value={note} onChange={(e) => setNote(e.target.value)} onBlur={() => note !== profile.time_note && setTime.mutate({ rating: n.time ?? "", note })} />
          ) : (
            <span>{profile.time_note || "—"}</span>
          )}
        </label>
      </div>
      <ErrorNote error={setTime.error} />
      <div className="flex flex-col gap-1 text-[12.5px]">
        <span className="flex items-baseline gap-2">
          <span className="label">{t("altro.ea.section.technologies")}</span>
          {profile.end_of_support_days !== null && (
            <span className={profile.end_of_support_days < 0 ? "font-semibold text-danger" : profile.end_of_support_days <= 180 ? "font-semibold text-flag" : "text-muted"}>
              {profile.end_of_support_days < 0 ? t("altro.ea.section.supportEnded", { count: -profile.end_of_support_days }) : t("altro.ea.section.supportEnds", { count: profile.end_of_support_days })}
            </span>
          )}
          {canWrite && !editTech && (
            <button className="ml-auto text-[12px] text-accent underline" onClick={() => setEditTech(true)}>
              {t("altro.ea.section.edit")}
            </button>
          )}
        </span>
        {!editTech &&
          (profile.technologies.length === 0 ? (
            <span className="text-muted">{t("altro.ea.section.noTech")}</span>
          ) : (
            <span className="flex flex-wrap gap-x-4 gap-y-1">
              {profile.technologies.map((tech) => (
                <span key={tech.id}>
                  {tech.name} {tech.version}
                  {(tech.end_of_support || tech.end_of_life) && <span className="text-muted"> · {t("altro.ea.section.until", { date: date(tech.end_of_support ?? tech.end_of_life) })}</span>}
                </span>
              ))}
            </span>
          ))}
        {editTech && (
          <div className="flex flex-col gap-2">
            <div className="flex max-h-48 flex-col overflow-y-auto">
              {techs.data?.items.map((tech) => (
                <label key={tech.id} className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={chosen.includes(tech.id)}
                    onChange={(e) => setChosen(e.target.checked ? [...chosen, tech.id] : chosen.filter((x) => x !== tech.id))}
                  />
                  {tech.name} {tech.version}
                </label>
              ))}
              {techs.data?.items.length === 0 && (
                <span className="text-muted">
                  <Trans
                    k="altro.ea.section.noCatalog"
                    slots={{
                      link: (
                        <Link to="/architettura/razionalizzazione" className="text-accent underline">
                          {t("altro.ea.tabs.rationalization")}
                        </Link>
                      ),
                    }}
                  />
                </span>
              )}
            </div>
            <span className="flex gap-2">
              <button className="btn btn-primary py-1" disabled={saveTech.isPending} onClick={() => saveTech.mutate()}>
                {t("altro.ea.section.save")}
              </button>
              <button className="btn py-1" onClick={() => setEditTech(false)}>
                {t("altro.ea.section.cancel")}
              </button>
            </span>
            <ErrorNote error={saveTech.error} />
          </div>
        )}
      </div>
    </div>
  );
}
