// Ciclo di vita e razionalizzazione (replica/architettura-ea.md §5): valutazione TIME,
// capability con più applicazioni, fine supporto vicina, applicazioni senza capability,
// tecnologie con le date di fine supporto e fine vita, impostazioni del tenant.
import { Select } from "../../select";
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMe } from "../../shell";
import { CatalogId } from "../../catalogId";
import { Criticality, ErrorNote, SectionTitle, date } from "../../ui";
import { TIME, canWriteEA, eaApi, type RatApp, type Settings, type Technology, type TimeRating } from "../../eaApi";
import { EaPage } from "./tabs";
import { useT } from "../../i18n/react";
import { tr } from "../../i18n/core";

const TIME_HELP = {
  get tolerate() {
    return tr("altro.ea.rat.helpTolerate");
  },
  get invest() {
    return tr("altro.ea.rat.helpInvest");
  },
  get migrate() {
    return tr("altro.ea.rat.helpMigrate");
  },
  get eliminate() {
    return tr("altro.ea.rat.helpEliminate");
  },
} as Record<TimeRating, string>;
const TIME_CLASS: Record<TimeRating, string> = { invest: "border-ok", tolerate: "border-accent", migrate: "border-flag", eliminate: "border-danger" };

function AppName({ a }: { a: RatApp }) {
  return (
    <Link to="/e/$key" params={{ key: a.key }} className="font-medium text-fg no-underline hover:underline">
      {a.name}
      <span className="ml-1.5 text-[11px] text-muted"><CatalogId code={a.code} itemKey={a.key} /></span>
    </Link>
  );
}

function days(n: number) {
  if (n < 0) return <span className="font-semibold text-danger">{tr("altro.ea.rat.expiredAgo", { count: -n })}</span>;
  if (n === 0) return <span className="font-semibold text-danger">{tr("altro.ea.rat.today")}</span>;
  return <span className={n <= 90 ? "font-semibold text-flag" : ""}>{tr("altro.ea.rat.inDays", { count: n })}</span>;
}

export function RationalizationPage() {
  const t = useT();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canWrite = canWriteEA(perms);
  const r = useQuery({ queryKey: ["ea", "rationalization"], queryFn: eaApi.rationalization });
  const d = r.data;
  return (
    <EaPage active="razionalizzazione" title={t("altro.ea.rat.title")}>
      <p className="m-0 max-w-3xl text-muted">{t("altro.ea.rat.intro")}</p>
      <ErrorNote error={r.error} />
      {d && (
        <>
          <div className="grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-5">
            {(Object.keys(TIME) as TimeRating[]).map((k) => (
              <div key={k} className={`flex flex-col border-l-4 ${TIME_CLASS[k]} pl-3`}>
                <span className="label">{TIME[k]}</span>
                <span className="font-mono text-[28px] leading-none font-bold tabular">{d.time_counts[k]}</span>
                <span className="text-[12px] text-muted">{TIME_HELP[k]}</span>
              </div>
            ))}
            <div className="flex flex-col border-l-4 border-hair pl-3">
              <span className="label">{t("altro.ea.rat.notRated")}</span>
              <span className="font-mono text-[28px] leading-none font-bold tabular">{d.time_counts.none}</span>
              <span className="text-[12px] text-muted">{t("altro.ea.rat.noTime")}</span>
            </div>
          </div>

          <section className="flex flex-col gap-2">
            <SectionTitle aside={<span className="text-[12px] text-muted">{t("altro.ea.rat.threshold", { count: d.settings.duplicate_min })}</span>}>{t("altro.ea.rat.multiApps")}</SectionTitle>
            {d.duplicates.length === 0 && <p className="m-0 text-muted">{t("altro.ea.rat.noMultiApps")}</p>}
            {d.duplicates.map((x) => (
              <div key={x.capability_id} className="flex flex-col gap-1 border-b border-hair py-2">
                <span>
                  <Link to="/architettura/capability" className="font-semibold text-fg no-underline hover:underline">
                    {x.capability_name}
                  </Link>
                  <span className="ml-2 text-[12px] text-muted">{x.path.slice(0, -1).join(" / ")}</span>
                </span>
                <span className="flex flex-wrap gap-x-4 gap-y-1 text-[12.5px]">
                  {x.applications.map((a) => (
                    <span key={a.id} className="flex items-baseline gap-1.5">
                      <AppName a={a} />
                      <span className="text-muted">{a.time ? TIME[a.time] : t("altro.ea.rat.timeToRate")}</span>
                    </span>
                  ))}
                </span>
              </div>
            ))}
          </section>

          <section className="flex flex-col gap-2">
            <SectionTitle aside={<span className="text-[12px] text-muted">{t("altro.ea.rat.notice", { count: d.settings.eol_warning_days })}</span>}>{t("altro.ea.rat.endOfSupportNear")}</SectionTitle>
            {d.end_of_support.length === 0 && <p className="m-0 text-muted">{t("altro.ea.rat.noExpiring")}</p>}
            {d.end_of_support.length > 0 && (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[620px] border-collapse text-[12.5px]">
                  <thead>
                    <tr className="label h-9 text-left">
                      <th scope="col" className="border-b-2 border-rule pr-3">{t("altro.ea.rat.application")}</th>
                      <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.rat.technology")}</th>
                      <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.rat.endOfSupport")}</th>
                      <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.rat.endOfLife")}</th>
                      <th scope="col" className="border-b-2 border-rule pl-3">{t("altro.ea.rat.expiry")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {d.end_of_support.map((x) => (
                      <tr key={`${x.application.id}-${x.technology_id}`} className="border-b border-hair">
                        <td className="py-1.5 pr-3">
                          <AppName a={x.application} />
                        </td>
                        <td className="px-3 py-1.5">{x.technology}</td>
                        <td className="px-3 py-1.5 font-mono">{x.end_of_support ? date(x.end_of_support) : "—"}</td>
                        <td className="px-3 py-1.5 font-mono">{x.end_of_life ? date(x.end_of_life) : "—"}</td>
                        <td className="py-1.5 pl-3">{days(x.days_left)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          <div className="grid gap-8 lg:grid-cols-2">
            <section className="flex flex-col gap-2">
              <SectionTitle>{t("altro.ea.rat.retiring")}</SectionTitle>
              {d.retiring_with_dependents.length === 0 && <p className="m-0 text-muted">{t("altro.ea.rat.none")}</p>}
              {d.retiring_with_dependents.map((a) => (
                <p key={a.id} className="m-0 flex items-baseline gap-2 border-b border-hair py-1.5 text-[12.5px]">
                  <span className="min-w-0 flex-1">
                    <AppName a={a} />
                  </span>
                  <span className="text-muted">{a.time ? TIME[a.time] : ""}</span>
                  <Link to="/architettura/mappa" search={{ nodo: a.key }} className="text-accent underline">
                    {t("altro.ea.rat.dependents", { count: a.dependents })}
                  </Link>
                </p>
              ))}
            </section>
            <section className="flex flex-col gap-2">
              <SectionTitle>{t("altro.ea.rat.orphans")}</SectionTitle>
              {d.orphans.length === 0 && <p className="m-0 text-muted">{t("altro.ea.rat.noOrphans")}</p>}
              <p className="m-0 flex flex-wrap gap-x-4 gap-y-1 text-[12.5px]">
                {d.orphans.map((a) => (
                  <AppName key={a.id} a={a} />
                ))}
              </p>
            </section>
          </div>

          <TimeTable apps={d.applications} canWrite={canWrite} />
          <Technologies canWrite={canWrite} />
          {canWrite && <SettingsForm settings={d.settings} />}
        </>
      )}
    </EaPage>
  );
}

function TimeTable({ apps, canWrite }: { apps: RatApp[]; canWrite: boolean }) {
  const t = useT();
  const [filter, setFilter] = useState("");
  const shown = apps.filter((a) => !filter || (filter === "none" ? !a.time : a.time === filter));
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle
        aside={
          <Select aria-label={t("altro.ea.rat.filterByRating")} className="field w-auto py-0.5 text-[12px]" value={filter} onChange={(e) => setFilter(e.target.value)}>
            <option value="">{t("altro.ea.rat.all")}</option>
            {Object.entries(TIME).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
            <option value="none">{t("altro.ea.rat.notRated")}</option>
          </Select>
        }
      >
        {t("altro.ea.rat.timeRating")}
      </SectionTitle>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[760px] border-collapse text-[12.5px]">
          <thead>
            <tr className="label h-9 text-left">
              <th scope="col" className="border-b-2 border-rule pr-3">{t("altro.ea.rat.application")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.rat.team")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.rat.criticality")}</th>
              <th scope="col" className="border-b-2 border-rule px-3 text-right">{t("altro.ea.rat.dependentsCol")}</th>
              {/* i18n-ok: sigla */}
              <th scope="col" className="border-b-2 border-rule px-3">TIME</th>
              <th scope="col" className="border-b-2 border-rule pl-3">{t("altro.ea.rat.rationale")}</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((a) => (
              <TimeRow key={a.id} app={a} canWrite={canWrite} />
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function TimeRow({ app, canWrite }: { app: RatApp; canWrite: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const [note, setNote] = useState(app.time_note);
  const save = useMutation({
    mutationFn: (v: { rating: string; note: string }) => eaApi.setTime(app.id, v.rating, v.note),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ea"] }),
  });
  return (
    <tr className="border-b border-hair">
      <td className="py-1.5 pr-3">
        <AppName a={app} />
      </td>
      <td className="px-3 py-1.5">{app.team ?? "—"}</td>
      <td className="px-3 py-1.5">
        <Criticality value={app.criticality} critical={app.dora} />
      </td>
      <td className="px-3 py-1.5 text-right font-mono">{app.dependents}</td>
      <td className="px-3 py-1">
        {canWrite ? (
          <Select
            aria-label={t("altro.ea.rat.ratingOf", { name: app.name })}
            className="field w-32 py-1"
            value={app.time ?? ""}
            disabled={save.isPending}
            onChange={(e) => save.mutate({ rating: e.target.value, note })}
          >
            <option value="">—</option>
            {Object.entries(TIME).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
          </Select>
        ) : app.time ? (
          TIME[app.time]
        ) : (
          "—"
        )}
      </td>
      <td className="py-1 pl-3">
        {canWrite ? (
          <input
            aria-label={t("altro.ea.rat.rationaleOf", { name: app.name })}
            className="field py-1"
            value={note}
            onChange={(e) => setNote(e.target.value)}
            onBlur={() => note !== app.time_note && save.mutate({ rating: app.time ?? "", note })}
          />
        ) : (
          app.time_note || "—"
        )}
        {save.error && <ErrorNote error={save.error} />}
      </td>
    </tr>
  );
}

function Technologies({ canWrite }: { canWrite: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const techs = useQuery({ queryKey: ["ea", "technologies"], queryFn: eaApi.technologies });
  const empty = { name: "", version: "", vendor: "", category: "", end_of_support: "", end_of_life: "" };
  const [f, setF] = useState(empty);
  const [editing, setEditing] = useState<string | null>(null);
  const done = () => qc.invalidateQueries({ queryKey: ["ea"] });
  const save = useMutation({
    mutationFn: () => (editing ? eaApi.updateTechnology(editing, f) : eaApi.createTechnology(f)),
    onSuccess: () => {
      setF(empty);
      setEditing(null);
      done();
    },
  });
  const remove = useMutation({ mutationFn: (id: string) => eaApi.deleteTechnology(id), onSuccess: done });
  const edit = (x: Technology) => {
    setEditing(x.id);
    setF({ name: x.name, version: x.version, vendor: x.vendor, category: x.category, end_of_support: x.end_of_support?.slice(0, 10) ?? "", end_of_life: x.end_of_life?.slice(0, 10) ?? "" });
  };
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{t("altro.ea.rat.technologies")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{t("altro.ea.rat.techIntro")}</p>
      {canWrite && (
        <form
          className="flex flex-wrap items-end gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <label className="flex min-w-0 flex-[1_1_160px] flex-col gap-1">
            <span className="label">{editing ? t("altro.ea.rat.editTech") : t("altro.ea.rat.newTech")}</span>
            <input className="field" required value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder={t("altro.ea.rat.techPlaceholder")} />
          </label>
          <label className="flex w-24 flex-col gap-1">
            <span className="label">{t("altro.ea.rat.version")}</span>
            <input className="field" value={f.version} onChange={(e) => setF({ ...f, version: e.target.value })} />
          </label>
          <label className="flex w-32 flex-col gap-1">
            <span className="label">{t("altro.ea.rat.vendor")}</span>
            <input className="field" value={f.vendor} onChange={(e) => setF({ ...f, vendor: e.target.value })} />
          </label>
          <label className="flex w-32 flex-col gap-1">
            <span className="label">{t("altro.ea.rat.category")}</span>
            <input className="field" list="ea-tech-cat" value={f.category} onChange={(e) => setF({ ...f, category: e.target.value })} />
            <datalist id="ea-tech-cat">
              {(["language", "framework", "database", "middleware", "os", "product"] as const).map((c) => (
                <option key={c} value={t(`altro.ea.rat.cat.${c}`)} />
              ))}
            </datalist>
          </label>
          <label className="flex w-40 flex-col gap-1">
            <span className="label">{t("altro.ea.rat.endOfSupport")}</span>
            <input className="field" type="date" value={f.end_of_support} onChange={(e) => setF({ ...f, end_of_support: e.target.value })} />
          </label>
          <label className="flex w-40 flex-col gap-1">
            <span className="label">{t("altro.ea.rat.endOfLife")}</span>
            <input className="field" type="date" value={f.end_of_life} onChange={(e) => setF({ ...f, end_of_life: e.target.value })} />
          </label>
          <button className="btn btn-primary" disabled={save.isPending}>
            {editing ? t("altro.ea.rat.save") : t("altro.ea.rat.add")}
          </button>
          {editing && (
            <button
              type="button"
              className="btn"
              onClick={() => {
                setEditing(null);
                setF(empty);
              }}
            >
              {t("altro.ea.rat.cancel")}
            </button>
          )}
          <div className="basis-full">
            <ErrorNote error={save.error ?? remove.error} />
          </div>
        </form>
      )}
      <div className="overflow-x-auto">
        <table className="w-full min-w-[640px] border-collapse text-[12.5px]">
          <thead>
            <tr className="label h-9 text-left">
              <th scope="col" className="border-b-2 border-rule pr-3">{t("altro.ea.rat.technology")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.rat.category")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.rat.endOfSupport")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.rat.endOfLife")}</th>
              <th scope="col" className="border-b-2 border-rule px-3 text-right">{t("altro.ea.rat.applications")}</th>
              <th scope="col" className="border-b-2 border-rule pl-3" />
            </tr>
          </thead>
          <tbody>
            {techs.data?.items.map((x) => (
              <tr key={x.id} className="border-b border-hair">
                <td className="py-1.5 pr-3">
                  <span className="font-medium">{x.name}</span> {x.version} {x.vendor && <span className="text-muted">· {x.vendor}</span>}
                </td>
                <td className="px-3 py-1.5">{x.category || "—"}</td>
                <td className="px-3 py-1.5 font-mono">{x.end_of_support ? date(x.end_of_support) : "—"}</td>
                <td className="px-3 py-1.5 font-mono">{x.end_of_life ? date(x.end_of_life) : "—"}</td>
                <td className="px-3 py-1.5 text-right font-mono">{x.applications}</td>
                <td className="py-1.5 pl-3 text-right whitespace-nowrap">
                  {x.days_left !== null && x.days_left <= 180 && <span className="mr-3">{days(x.days_left)}</span>}
                  {canWrite && (
                    <span className="inline-flex gap-3 text-[12px]">
                      <button className="text-accent underline" onClick={() => edit(x)}>
                        {t("altro.ea.rat.edit")}
                      </button>
                      <button className="text-muted underline hover:text-danger" onClick={() => remove.mutate(x.id)}>
                        {t("altro.ea.rat.delete")}
                      </button>
                    </span>
                  )}
                </td>
              </tr>
            ))}
            {techs.data?.items.length === 0 && (
              <tr>
                <td colSpan={6} className="py-6 text-muted">
                  {t("altro.ea.rat.noTechnologies")}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function SettingsForm({ settings }: { settings: Settings }) {
  const t = useT();
  const qc = useQueryClient();
  const [f, setF] = useState({ node_types: settings.node_types.join(", "), eol: settings.eol_warning_days, dup: settings.duplicate_min });
  const save = useMutation({
    mutationFn: () =>
      eaApi.updateSettings({
        node_types: f.node_types
          .split(/[,\s]+/)
          .map((s) => s.trim())
          .filter(Boolean),
        eol_warning_days: Number(f.eol),
        duplicate_min: Number(f.dup),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ea"] }),
  });
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{t("altro.ea.rat.settings")}</SectionTitle>
      <form
        className="flex flex-wrap items-end gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <label className="flex min-w-0 flex-[1_1_320px] flex-col gap-1">
          <span className="label">{t("altro.ea.rat.nodeTypes")}</span>
          <input className="field font-mono" value={f.node_types} onChange={(e) => setF({ ...f, node_types: e.target.value })} />
        </label>
        <label className="flex w-44 flex-col gap-1">
          <span className="label">{t("altro.ea.rat.noticeDays")}</span>
          <input className="field" type="number" min={0} max={3650} value={f.eol} onChange={(e) => setF({ ...f, eol: Number(e.target.value) })} />
        </label>
        <label className="flex w-44 flex-col gap-1">
          <span className="label">{t("altro.ea.rat.duplicatesFrom")}</span>
          <input className="field" type="number" min={2} max={50} value={f.dup} onChange={(e) => setF({ ...f, dup: Number(e.target.value) })} />
        </label>
        <button className="btn btn-primary" disabled={save.isPending}>
          {t("altro.ea.rat.save")}
        </button>
        <div className="basis-full">
          <ErrorNote error={save.error} />
          {save.isSuccess && <p className="m-0 text-[12.5px] text-ok">{t("altro.ea.rat.settingsSaved")}</p>}
        </div>
      </form>
    </section>
  );
}
