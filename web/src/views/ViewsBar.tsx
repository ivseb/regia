// Barra delle viste salvate (piano, passo 16): scelta della vista, "salva come vista",
// rinomina, condivisione, duplicazione, predefinita e preferiti del menu.
import { Select } from "../select";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ErrorNote } from "../ui";
import { useT } from "../i18n/react";
import { viewsApi, type SavedView, type ViewKind, type ViewScope, type ViewSettings } from "./viewsApi";

export type Current = { query: string; kind: ViewKind; settings: ViewSettings };

function useOutside(open: boolean, close: () => void) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const out = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && close();
    window.addEventListener("mousedown", out);
    return () => window.removeEventListener("mousedown", out);
  }, [open, close]);
  return ref;
}

export function ViewsBar({
  page,
  views,
  active,
  modified,
  current,
  isAdmin,
  onApply,
  onReset,
  onSaved,
  onCopyLink,
}: {
  page: string;
  views: SavedView[];
  active?: SavedView;
  modified: boolean;
  current: Current;
  isAdmin: boolean;
  /** Applica una vista (null = la configurazione di base della pagina). */
  onApply: (v: SavedView | null) => void;
  onReset: () => void;
  /** Dopo un salvataggio (nuova vista o aggiornamento); null se la vista è stata eliminata. */
  onSaved: (v: SavedView | null) => void;
  onCopyLink: () => Promise<boolean>;
}) {
  const t = useT();
  const qc = useQueryClient();
  const [menu, setMenu] = useState<"" | "views" | "more">("");
  const [form, setForm] = useState<"" | "new" | "edit">("");
  const [confirmDel, setConfirmDel] = useState(false);
  const [copied, setCopied] = useState(false);
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["views", page] });
    qc.invalidateQueries({ queryKey: ["views", "favorites"] });
  };
  const [error, setError] = useState<unknown>(null);
  const mut = <T,>(fn: () => Promise<T>, then?: (r: T) => void) => // i18n-ok: codice TypeScript letto come testo
    fn().then(
      (r) => {
        setError(null);
        refresh();
        then?.(r);
      }, // i18n-ok: codice TypeScript letto come testo
      (e) => setError(e),
    );

  const viewsRef = useOutside(menu === "views", () => setMenu(""));
  const moreRef = useOutside(menu === "more", () => setMenu(""));
  const mine = views.filter((v) => v.mine);
  const shared = views.filter((v) => !v.mine);

  const star = (v: SavedView) => mut(() => viewsApi.favorite(v.id, !v.favorite));

  const item = (v: SavedView) => (
    <li key={v.id} className={`flex items-center border-b border-hair last:border-b-0 ${active?.id === v.id ? "bg-sel" : "hover:bg-sel/60"}`}>
      <button
        className="flex min-w-0 flex-1 items-baseline gap-2 px-3 py-1.5 text-left"
        aria-current={active?.id === v.id ? "true" : undefined}
        onClick={() => {
          setMenu("");
          onApply(v);
        }}
      >
        <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{v.name}</span>
        {(v.default_me || v.default_tenant) && <span className="text-[11px] text-muted">{v.default_me ? t("altro.views.default") : t("altro.views.defaultOrg")}</span>}
        <span className="text-[11px] text-muted">{v.scope === "group" ? (v.group_name ?? t("altro.views.scope.group")) : t(`altro.views.scope.${v.scope}`)}</span>
      </button>
      <button
        className={`px-2.5 py-1.5 text-[15px] leading-none ${v.favorite ? "text-flag" : "text-muted hover:text-fg"}`}
        aria-pressed={v.favorite}
        aria-label={v.favorite ? t("altro.views.unstar", { name: v.name }) : t("altro.views.star", { name: v.name })}
        title={v.favorite ? t("altro.views.starTitleOn") : t("altro.views.starTitleOff")}
        onClick={() => star(v)}
      >
        {v.favorite ? "★" : "☆"}
      </button>
    </li>
  ); // i18n-ok: codice TypeScript letto come testo

  return (
    <div className="mx-4 mt-3 flex flex-wrap items-center gap-2 md:mx-7">
      <div ref={viewsRef} className="relative min-w-0">
        <button className="btn flex max-w-full items-center gap-2 py-1 text-[12.5px]" aria-expanded={menu === "views"} aria-haspopup="true" onClick={() => setMenu(menu === "views" ? "" : "views")}>
          <span className="text-muted">{t("altro.views.view")}</span>
          <span className="min-w-0 font-semibold [overflow-wrap:anywhere]">{active ? active.name : t("altro.views.pageDefault")}</span>
          {modified && <span className="border border-flag px-1 text-[11px] text-flag">{t("altro.views.modified")}</span>}
          <span aria-hidden="true" className="font-mono text-[10px]">▾</span>
        </button>
        {menu === "views" && (
          <div className="absolute top-full left-0 z-40 mt-1 w-[360px] max-w-[calc(100vw-32px)] border-[1.5px] border-rule bg-bg">
            <ul className="m-0 max-h-80 list-none overflow-y-auto p-0">
              <li className={`border-b border-hair ${!active ? "bg-sel" : "hover:bg-sel/60"}`}>
                <button className="w-full px-3 py-1.5 text-left" onClick={() => { setMenu(""); onApply(null); }}>
                  {t("altro.views.pageDefault")}
                </button>
              </li>
              {mine.length > 0 && <li className="label border-b border-hair bg-sel/40 px-3 py-1">{t("altro.views.mine")}</li>}
              {mine.map(item)}
              {shared.length > 0 && <li className="label border-b border-hair bg-sel/40 px-3 py-1">{t("altro.views.sharedWithMe")}</li>}
              {shared.map(item)}
              {views.length === 0 && <li className="px-3 py-2 text-[12.5px] text-muted">{t("altro.views.none")}</li>}
            </ul>
          </div>
        )}
      </div>

      {active && (
        <button
          className={`px-1 text-[17px] leading-none ${active.favorite ? "text-flag" : "text-muted hover:text-fg"}`}
          aria-pressed={active.favorite}
          aria-label={active.favorite ? t("altro.views.unstarView") : t("altro.views.starView")}
          title={active.favorite ? t("altro.views.starViewTitleOn") : t("altro.views.starViewTitleOff")}
          onClick={() => star(active)}
        >
          {active.favorite ? "★" : "☆"}
        </button>
      )}

      {modified && active?.can_edit && (
        <button className="btn btn-primary py-1 text-[12.5px]" onClick={() => mut(() => viewsApi.update(active.id, { ...current }), (v) => onSaved(v))}>
          {t("altro.views.saveView")}
        </button>
      )}
      {(modified || !active) && (
        <button className="btn py-1 text-[12.5px]" onClick={() => setForm(form === "new" ? "" : "new")} aria-expanded={form === "new"}>
          {t("altro.views.saveAs")}
        </button>
      )}
      {modified && active && (
        <button className="text-[12.5px] text-muted underline hover:text-fg" onClick={onReset}>
          {t("altro.views.revert")}
        </button>
      )}

      <span className="ml-auto flex items-center gap-2">
        <button
          className="btn py-1 text-[12.5px]"
          title={t("altro.views.copyLinkTitle")}
          onClick={() =>
            onCopyLink().then((ok) => {
              setCopied(ok);
              setTimeout(() => setCopied(false), 2000);
            })
          }
        >
          {copied ? t("altro.views.linkCopied") : t("altro.views.copyLink")}
        </button>
        {active && (
          <div ref={moreRef} className="relative">
            <button className="btn py-1 text-[12.5px]" aria-expanded={menu === "more"} aria-haspopup="true" aria-label={t("altro.views.actions")} onClick={() => setMenu(menu === "more" ? "" : "more")}>
              ⋯
            </button>
            {menu === "more" && (
              <ul className="absolute top-full right-0 z-40 m-0 mt-1 w-[280px] max-w-[calc(100vw-32px)] list-none border-[1.5px] border-rule bg-bg p-0">
                {active.can_edit && (
                  <li>
                    <button className="w-full border-b border-hair px-3 py-1.5 text-left hover:bg-sel/60" onClick={() => { setMenu(""); setForm("edit"); }}>
                      {t("altro.views.rename")}
                    </button>
                  </li>
                )}
                <li>
                  <button
                    className="w-full border-b border-hair px-3 py-1.5 text-left hover:bg-sel/60"
                    onClick={() => { setMenu(""); mut(() => viewsApi.duplicate(active.id), (v) => onSaved(v)); }}
                  >
                    {t("altro.views.duplicate")}
                  </button>
                </li>
                <li>
                  <button
                    className="w-full border-b border-hair px-3 py-1.5 text-left hover:bg-sel/60"
                    onClick={() => { setMenu(""); mut(() => viewsApi.setDefault(page, active.default_me ? null : active.id, "me")); }}
                  >
                    {active.default_me ? t("altro.views.unsetMine") : t("altro.views.setMine")}
                  </button>
                </li>
                {isAdmin && active.scope === "tenant" && (
                  <li>
                    <button
                      className="w-full border-b border-hair px-3 py-1.5 text-left hover:bg-sel/60"
                      onClick={() => { setMenu(""); mut(() => viewsApi.setDefault(page, active.default_tenant ? null : active.id, "tenant")); }}
                    >
                      {active.default_tenant ? t("altro.views.unsetAll") : t("altro.views.setAll")}
                    </button>
                  </li>
                )}
                {active.can_edit && (
                  <li>
                    <button className="w-full px-3 py-1.5 text-left text-danger hover:bg-sel/60" onClick={() => { setMenu(""); setConfirmDel(true); }}>
                      {t("altro.views.deleteMenu")}
                    </button>
                  </li>
                )}
              </ul>
            )}
          </div>
        )}
      </span>

      {confirmDel && active && (
        <div role="alertdialog" aria-label={t("altro.views.deleteTitle")} className="flex w-full flex-wrap items-center gap-3 border border-danger px-3 py-2 text-[12.5px]">
          <span>{t(active.scope !== "private" ? "altro.views.deleteAskShared" : "altro.views.deleteAsk", { name: active.name })}</span>
          <button className="btn py-0.5" onClick={() => { setConfirmDel(false); mut(() => viewsApi.remove(active.id), () => onSaved(null)); }}>{t("altro.views.delete")}</button>
          <button className="text-muted underline" onClick={() => setConfirmDel(false)}>{t("altro.views.cancel")}</button>
        </div>
      )}
      {form && (
        <ViewForm
          mode={form}
          active={active}
          isAdmin={isAdmin}
          onCancel={() => setForm("")}
          onSubmit={(name, scope, groupId) => {
            const p = form === "new"
              ? viewsApi.create({ page, name, scope, group_id: groupId, ...current })
              : viewsApi.update(active!.id, { name, scope, group_id: groupId });
            return mut(() => p, (v) => { setForm(""); onSaved(v); });
          }}
        />
      )}
      {!!error && <div className="w-full"><ErrorNote error={error} /></div>}
    </div>
  ); // i18n-ok: codice TypeScript letto come testo
}

/** Nome e condivisione di una vista, per salvarne una nuova o cambiare quella aperta. */
function ViewForm({ mode, active, isAdmin, onSubmit, onCancel }: { mode: "new" | "edit"; active?: SavedView; isAdmin: boolean; onSubmit: (name: string, scope: ViewScope, groupId: string | null) => Promise<void>; onCancel: () => void }): ReactNode { // i18n-ok: codice TypeScript letto come testo
  const t = useT();
  const [name, setName] = useState(mode === "edit" ? (active?.name ?? "") : "");
  const [scope, setScope] = useState<ViewScope>(mode === "edit" ? (active?.scope ?? "private") : "private");
  const [groupId, setGroupId] = useState<string>(active?.group_id ?? "");
  const groups = useQuery({ queryKey: ["views", "groups"], queryFn: viewsApi.groups, staleTime: 60_000 });
  const list = groups.data?.items ?? [];
  const effGroup = groupId || list[0]?.id || "";
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => input.current?.focus(), []);
  return (
    <form
      className="flex w-full flex-wrap items-end gap-3 border-[1.5px] border-rule p-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (name.trim()) onSubmit(name.trim(), scope, scope === "group" ? effGroup : null);
      }}
    >
      <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
        <span className="label">{mode === "new" ? t("altro.views.nameNew") : t("altro.views.name")}</span>
        <input ref={input} className="field" value={name} maxLength={80} onChange={(e) => setName(e.target.value)} placeholder={t("altro.views.namePlaceholder")} />
      </label>
      <fieldset className="m-0 flex flex-wrap items-center gap-x-4 gap-y-1 border-0 p-0">
        <legend className="label mb-1 p-0">{t("altro.views.sharedWith")}</legend>
        <label className="flex items-center gap-1.5"><input type="radio" name="scope" checked={scope === "private"} onChange={() => setScope("private")} /> {t("altro.views.onlyMe")}</label>
        <label className="flex items-center gap-1.5">
          <input type="radio" name="scope" checked={scope === "group"} disabled={list.length === 0} onChange={() => setScope("group")} /> {t("altro.views.aGroup")}
        </label>
        {scope === "group" && (
          <Select className="field py-1" aria-label={t("altro.views.group")} value={effGroup} onChange={(e) => setGroupId(e.target.value)}>
            {list.map((g) => (
              <option key={g.id} value={g.id}>{g.name}</option>
            ))}
          </Select>
        )}
        {isAdmin && <label className="flex items-center gap-1.5"><input type="radio" name="scope" checked={scope === "tenant"} onChange={() => setScope("tenant")} /> {t("altro.views.wholeOrg")}</label>}
      </fieldset>
      <span className="flex items-center gap-3">
        <button type="submit" className="btn btn-primary py-1" disabled={!name.trim()}>
          {mode === "new" ? t("altro.views.saveView") : t("altro.views.save")}
        </button>
        <button type="button" className="text-muted underline" onClick={onCancel}>{t("altro.views.cancel")}</button>
      </span>
    </form>
  );
}
