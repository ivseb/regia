// Tastiera e creazione globale (replica/usabilita.md, proposta 10): il pannello «?» con l'elenco
// delle scorciatoie, il dialogo «+ Crea» (un solo punto per creare qualunque tipo permesso, con i
// campi essenziali) e le azioni sull'elemento aperto (assegna a me, segui, copia il link,
// commenta). Si carica alla prima apertura, come la palette.
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api, collab, type Element, type Me } from "./api";
import { workApi } from "./workApi";
import { kb } from "./knowledgeApi";
import { riskApi } from "./riskApi";
import { Dialog } from "./pages/work-ui";
import { Select } from "./select";
import { ErrorNote } from "./ui";
import { useT } from "./i18n/react";
import { getLang, tr } from "./i18n/core";
import type { Key } from "./i18n/core";
import { createKinds, GO_KEYS, isWorkKind, SHORTCUT, useKindLabel, type CreateKind, type ItemAction } from "./shortcuts";
import { useWorkTypes } from "./typesApi";

// ---------------------------------------------------------------------
// Pannello «?»

const ROWS: { title: Key; rows: { keys: string[]; label: Key }[] }[] = [
  {
    title: "tastiera.help.general",
    rows: [
      { keys: [SHORTCUT], label: "tastiera.help.search" },
      { keys: ["/"], label: "tastiera.help.searchSlash" },
      { keys: ["c"], label: "tastiera.help.create" },
      { keys: ["?"], label: "tastiera.help.help" },
      { keys: ["Esc"], label: "tastiera.help.close" },
    ],
  },
  {
    title: "tastiera.help.lists",
    rows: [
      { keys: ["j", "k"], label: "tastiera.help.nextPrev" },
      { keys: ["Enter", "o"], label: "tastiera.help.openRow" },
    ],
  },
  {
    title: "tastiera.help.item",
    rows: [
      { keys: ["a"], label: "tastiera.help.assign" },
      { keys: ["s"], label: "tastiera.help.phase" },
      { keys: ["m"], label: "tastiera.help.comment" },
      { keys: ["f"], label: "tastiera.help.follow" },
      { keys: ["l"], label: "tastiera.help.copy" },
    ],
  },
];

function Kbd({ children }: { children: string }) {
  return <kbd className="inline-block min-w-[22px] border border-rule px-1.5 py-px text-center font-mono text-[12px] leading-[18px]">{children}</kbd>;
}

export function ShortcutsPanel({ onClose, allowed }: { onClose: () => void; allowed: ReadonlySet<string> }) {
  const t = useT();
  const go = GO_KEYS.filter((g) => allowed.has(g.to) || (g.to === "/tempo" && allowed.has("/tempo/report")));
  return (
    <Dialog title={t("tastiera.help.title")} onClose={onClose} wide>
      <div className="grid gap-x-8 gap-y-4 md:grid-cols-2">
        {ROWS.map((g) => (
          <section key={g.title} aria-label={t(g.title)} className="flex flex-col">
            <h3 className="label m-0 border-b-2 border-rule pb-1">{t(g.title)}</h3>
            <dl className="m-0">
              {g.rows.map((r) => (
                <div key={r.label} className="flex items-baseline justify-between gap-3 border-b border-hair py-1.5">
                  <dt className="min-w-0">{t(r.label)}</dt>
                  <dd className="m-0 flex shrink-0 gap-1">
                    {r.keys.map((k) => (
                      <Kbd key={k}>{k}</Kbd>
                    ))}
                  </dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
        <section aria-label={t("tastiera.help.goTo")} className="flex flex-col">
          <h3 className="label m-0 border-b-2 border-rule pb-1">{t("tastiera.help.goTo")}</h3>
          <dl className="m-0">
            {go.map((g) => (
              <div key={g.key} className="flex items-baseline justify-between gap-3 border-b border-hair py-1.5">
                <dt className="min-w-0">{t(g.label)}</dt>
                <dd className="m-0 flex shrink-0 items-center gap-1">
                  <Kbd>g</Kbd>
                  <span className="text-muted" aria-hidden="true">+</span>
                  <Kbd>{g.key}</Kbd>
                </dd>
              </div>
            ))}
          </dl>
        </section>
      </div>
      <p className="m-0 text-[12.5px] text-muted">{t("tastiera.help.note")}</p>
    </Dialog>
  );
}

// ---------------------------------------------------------------------
// «+ Crea»


/** Il tipo da proporre per primo, secondo la pagina in cui ci si trova. */
function defaultKind(path: string, kinds: CreateKind[]): CreateKind | undefined {
  const pick = (k: CreateKind) => (kinds.includes(k) ? k : undefined);
  if (path.startsWith("/delivery")) return pick("story");
  if (path.startsWith("/documenti")) return pick("document");
  if (path.startsWith("/rischi")) return pick("risk");
  return pick("request") ?? kinds[0];
}

export function CreateDialog({ kind, onClose, notify }: { kind?: CreateKind; onClose: () => void; notify: (text: string) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const path = useRouterState({ select: (s) => s.location.pathname });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me }).data;
  const workTypes = useWorkTypes();
  const base = useMemo(() => createKinds(me, workTypes.creatable), [me, workTypes.creatable]);
  const spacesWork = useQuery({ queryKey: ["work", "spaces"], queryFn: workApi.spaces, enabled: base.some(isWorkKind), staleTime: 60_000 });
  const spacesDoc = useQuery({ queryKey: ["kb", "spaces"], queryFn: kb.spaces, enabled: base.includes("document"), staleTime: 60_000 });
  const workSpaces = spacesWork.data?.items ?? [];
  const docSpaces = (spacesDoc.data?.items ?? []).filter((s) => s.rights.write);
  // Senza uno spazio in cui scrivere il tipo non si offre (una volta noto).
  const kinds = base.filter((k) => (isWorkKind(k) ? !spacesWork.isSuccess || workSpaces.length > 0 : k === "document" ? !spacesDoc.isSuccess || docSpaces.length > 0 : true));
  const [chosen, setChosen] = useState<CreateKind | undefined>(kind);
  const current: CreateKind | undefined = chosen && kinds.includes(chosen) ? chosen : (defaultKind(path, kinds) ?? kinds[0]);
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [space, setSpace] = useState("");
  const titleRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    // Il titolo ha il fuoco anche dopo il cambio di tipo.
    titleRef.current?.focus();
  }, [current]);

  const isWork = isWorkKind(current);
  const spaceList = isWork ? workSpaces.map((s) => ({ key: s.key, name: `${s.key} · ${s.name}` })) : current === "document" ? docSpaces.map((s) => ({ key: s.key, name: s.name })) : [];
  const spaceKey = spaceList.some((s) => s.key === space) ? space : (spaceList[0]?.key ?? "");
  const hasDescription = current === "request" || current === "risk" || current === "incident";

  const label = useKindLabel();

  const create = useMutation({
    mutationFn: async (): Promise<{ key: string; document?: boolean }> => {
      const name = title.trim();
      const text = description.trim();
      switch (current) {
        case "request": {
          const el = await api.createRequest({ title: name, description: text });
          return { key: el.key };
        }
        case "document": {
          const p = await kb.createPage(spaceKey, { title: name });
          return { key: p.page.key, document: true };
        }
        case "risk": {
          const r = await riskApi.createRisk({ title: name, description: text || undefined });
          return { key: r.key };
        }
        case "incident": {
          const r = await riskApi.createIncident({ title: name, description: text || undefined });
          return { key: r.key };
        }
      }
      if (!isWorkKind(current)) throw new Error("kind");
      const el: Element = await workApi.createItem(spaceKey, { type: current!, title: name });
      return { key: el.key };
    },
    onSuccess: (r) => {
      qc.invalidateQueries();
      notify(t("tastiera.created", { key: r.key }));
      onClose();
      if (r.document) navigate({ to: "/documenti/$key", params: { key: r.key }, search: { vista: "modifica" } as never });
      else navigate({ to: "/e/$key", params: { key: r.key } });
    },
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (title.trim() && current && (spaceList.length === 0 || spaceKey) && !create.isPending) create.mutate();
  };
  const canSubmit = !!current && !!title.trim() && (!(isWork || current === "document") || !!spaceKey);

  return (
    <Dialog title={t("tastiera.create.title")} onClose={onClose}>
      {kinds.length === 0 ? (
        <p className="m-0 text-muted">{base.length === 0 || (spacesWork.isSuccess && spacesDoc.isSuccess) || !me ? t("tastiera.create.none") : t("tastiera.create.loading")}</p>
      ) : (
        <form className="flex flex-col gap-3" onSubmit={submit}>
          <label className="flex flex-col gap-1">
            <span className="label">{t("tastiera.create.kind")}</span>
            <Select value={current ?? ""} onChange={(e) => setChosen(e.target.value as CreateKind)}>
              {kinds.map((k) => (
                <option key={k} value={k}>{label(k)}</option>
              ))}
            </Select>
          </label>
          {(isWork || current === "document") && (
            <label className="flex flex-col gap-1">
              <span className="label">{isWork ? t("tastiera.create.space") : t("tastiera.create.docSpace")}</span>
              <Select value={spaceKey} onChange={(e) => setSpace(e.target.value)}>
                {spaceList.map((s) => (
                  <option key={s.key} value={s.key}>{s.name}</option>
                ))}
              </Select>
            </label>
          )}
          <label className="flex flex-col gap-1">
            <span className="label">{t("tastiera.create.name")}</span>
            <input ref={titleRef} className="field" value={title} maxLength={200} required onChange={(e) => setTitle(e.target.value)} />
          </label>
          {hasDescription && (
            <label className="flex flex-col gap-1">
              <span className="label">{t("tastiera.create.description")}</span>
              <textarea
                className="field min-h-20"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                    e.preventDefault();
                    e.currentTarget.form?.requestSubmit();
                  }
                }}
              />
            </label>
          )}
          <ErrorNote error={create.error} />
          <div className="flex items-center gap-2">
            <button className="btn btn-primary" disabled={!canSubmit || create.isPending}>
              {create.isPending ? t("tastiera.create.creating") : t("tastiera.create.submit")}
            </button>
            <button type="button" className="btn" onClick={onClose}>{t("tastiera.create.cancel")}</button>
            <span className="ml-auto hidden font-mono text-[11.5px] text-muted md:inline">{t("tastiera.create.hint")}</span>
          </div>
        </form>
      )}
    </Dialog>
  );
}

// ---------------------------------------------------------------------
// Azioni sull'elemento aperto o sulla riga evidenziata

export type ItemContext = {
  me: Me | undefined;
  qc: QueryClient;
  notify: (text: string) => void;
  navigate: (to: string, hash?: string) => void;
};

const linkOf = (key: string) => `${window.location.origin}/e/${encodeURIComponent(key)}`;

/** Copia negli appunti; se il browser non lo permette mostra l'indirizzo da copiare. */
export async function copyLink(key: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(linkOf(key));
    return true;
  } catch {
    window.prompt(tr("tastiera.copyPrompt"), linkOf(key));
    return false;
  }
}

/** «Assegna a me»: vale per gli elementi di lavoro (i tipi del modulo work, sprint escluso). */
export async function assignToMe(key: string, ctx: Pick<ItemContext, "me" | "qc" | "notify">): Promise<boolean> {
  const memberId = ctx.me?.active_tenant?.member_id;
  const detail = await ctx.qc.fetchQuery({ queryKey: ["element", key], queryFn: () => api.element(key), staleTime: 30_000 });
  const mine = detail.element.assignee_id === memberId;
  const names = ctx.qc.getQueryData<{ items: { key: string; module: string }[] }>(["type-names", getLang()])?.items;
  const isWork = names ? names.some((n) => n.key === detail.element.type && n.module === "work" && n.key !== "sprint") : ["epic", "story", "task", "bug"].includes(detail.element.type);
  if (!memberId || !isWork) {
    ctx.notify(tr("tastiera.assignNo", { key }));
    return false;
  }
  if (mine) {
    ctx.notify(tr("tastiera.assignAlready", { key }));
    return true;
  }
  await workApi.assign(key, memberId);
  ctx.qc.invalidateQueries();
  ctx.notify(tr("tastiera.assigned", { key }));
  return true;
}

export async function runItemAction(action: Exclude<ItemAction, "phase">, key: string, ctx: ItemContext) {
  try {
    switch (action) {
      case "assign":
        await assignToMe(key, ctx);
        break;
      case "follow": {
        const w = await collab.watch(key);
        const next = await collab.setWatch(key, !w.following);
        ctx.qc.setQueryData(["watch", key], next);
        ctx.notify(tr(next.following ? "tastiera.following" : "tastiera.unfollowed", { key }));
        break;
      }
      case "copy":
        if (await copyLink(key)) ctx.notify(tr("tastiera.linkCopied", { key }));
        break;
      case "comment": {
        ctx.navigate(`/e/${encodeURIComponent(key)}`, "commenti");
        // Il campo del commento compare quando la pagina ha caricato i dati.
        const until = Date.now() + 4000;
        const tick = () => {
          const box = document.querySelector<HTMLTextAreaElement>("#commenti textarea");
          if (box) box.focus();
          else if (Date.now() < until) setTimeout(tick, 120);
        };
        setTimeout(tick, 120);
        break;
      }
    }
  } catch (e) {
    ctx.notify(e instanceof Error && e.message ? e.message : tr("errors.network"));
  }
}

/** Livello caricato alla prima apertura: il pannello «?» e il dialogo «+ Crea». */
export function KeyboardLayer({
  help,
  create,
  allowed,
  onClose,
  notify,
}: {
  help: boolean;
  create: { kind?: CreateKind } | null;
  allowed: ReadonlySet<string>;
  onClose: () => void;
  notify: (text: string) => void;
}) {
  return (
    <>
      {help && <ShortcutsPanel onClose={onClose} allowed={allowed} />}
      {create && <CreateDialog kind={create.kind} onClose={onClose} notify={notify} />}
    </>
  );
}
