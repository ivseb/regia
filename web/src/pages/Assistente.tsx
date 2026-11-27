// Pannello «Assistente» (replica/ai.md, ondata 1: primo uso). La persona fa una domanda,
// l'assistente legge con gli strumenti e con i permessi della persona e risponde con i link
// agli elementi; le scritture arrivano come proposte da confermare o rifiutare qui. Compare
// solo con l'AI accesa e un modello configurato: senza, il resto dell'app non cambia.
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { aiApi, askStream, type AskEvent, type AskResult, type ChatMessage, type GraphResult, type Proposal, type Ref } from "../aiApi";
import { api } from "../api";
import { viewsApi } from "../views/viewsApi";
import { Select } from "../select";
import { WikiAdd } from "./WikiAdd";
import { ErrorNote, Loading } from "../ui";
import { useT } from "../i18n/react";
import { lookup } from "../i18n/core";
import { fmtNumber } from "../i18n/format";

function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** Il testo della risposta con le chiavi degli elementi citati trasformate in link. */
export function LinkedText({ text, refs }: { text: string; refs: Ref[] }) {
  const parts = useMemo(() => {
    const keys = [...new Set(refs.map((r) => r.key).filter(Boolean))].sort((a, b) => b.length - a.length);
    if (keys.length === 0) return [text];
    const re = new RegExp(`(${keys.map(escapeRe).join("|")})`, "g");
    const out: ReactNode[] = [];
    let last = 0;
    for (const m of text.matchAll(re)) {
      const i = m.index ?? 0;
      if (i > last) out.push(text.slice(last, i));
      out.push(
        <Link key={`${i}-${m[0]}`} to="/e/$key" params={{ key: m[0] }} className="font-mono font-semibold">
          {m[0]}
        </Link>,
      );
      last = i + m[0].length;
    }
    if (last < text.length) out.push(text.slice(last));
    return out;
  }, [text, refs]);
  return <p className="m-0 whitespace-pre-wrap [overflow-wrap:anywhere]">{parts}</p>;
}

function ProposalCard({ p, onDecided }: { p: Proposal; onDecided: (p: Proposal) => void }) {
  const t = useT();
  const decide = useMutation({
    mutationFn: (confirm: boolean) => (confirm ? aiApi.confirm(p.id) : aiApi.reject(p.id)),
    onSuccess: onDecided,
  });
  const status = lookup(`ai.proposal.status.${p.status}`) ?? p.status;
  return (
    <div className="flex flex-col gap-2 border-2 border-rule p-3" data-testid="proposta">
      <span className="label">{t("ai.proposal.title")}</span>
      <span className="[overflow-wrap:anywhere]">{p.summary}</span>
      <span className={`text-[12.5px] ${p.status === "applied" ? "text-ok" : p.status === "failed" ? "text-danger" : "text-muted"}`} role="status">
        {status}
        {p.error ? ` · ${p.error}` : ""}
      </span>
      <ErrorNote error={decide.error} />
      {p.status === "pending" && (
        <div className="flex flex-wrap gap-2">
          <button className="btn btn-primary" disabled={decide.isPending} onClick={() => decide.mutate(true)}>
            {t("ai.proposal.confirm")}
          </button>
          <button className="btn" disabled={decide.isPending} onClick={() => decide.mutate(false)}>
            {t("ai.proposal.reject")}
          </button>
        </div>
      )}
    </div>
  );
}

function Message({ m, onProposal }: { m: ChatMessage; onProposal: (p: Proposal) => void }) {
  const t = useT();
  if (m.role === "user") {
    return (
      <div className="ml-8 flex flex-col gap-1 self-end border border-hair bg-side px-3 py-2" data-testid="domanda">
        <span className="sr-only">{t("ai.panel.you")}</span>
        <p className="m-0 whitespace-pre-wrap [overflow-wrap:anywhere]">{m.text}</p>
      </div>
    );
  }
  const tools = m.tools.map((n) => lookup(`ai.tool.${n}`) ?? n);
  return (
    <div className="mr-4 flex flex-col gap-2" data-testid="risposta">
      <span className="label">{t("ai.panel.assistant")}</span>
      <LinkedText text={m.text} refs={m.refs} />
      {m.refs.length > 0 && (
        <ul className="m-0 flex list-none flex-col gap-1 p-0" aria-label={t("ai.panel.refs")}>
          {m.refs.map((r) => (
            <li key={r.id} className="text-[12.5px]">
              <Link to="/e/$key" params={{ key: r.key }}>
                <span className="font-mono">{r.key}</span> {r.title}
              </Link>
            </li>
          ))}
        </ul>
      )}
      {m.proposals.map((p) => (
        <ProposalCard key={p.id} p={p} onDecided={onProposal} />
      ))}
      {tools.length > 0 && <span className="text-[12px] text-muted">{t("ai.panel.usedTools", { tools: tools.join(", ") })}</span>}
      {/* Wiki personale (ondata 4): la risposta si aggiunge a mano alla wiki. */}
      {m.text.trim() && <WikiAdd text={m.text} compact />}
    </div>
  );
}

/** Risposta a una domanda sul grafo: la query visibile e modificabile, l'elenco con i link, il salvataggio come vista. */
function GraphAnswer({ r }: { r: GraphResult }) {
  const t = useT();
  const qc = useQueryClient();
  const [query, setQuery] = useState(r.query);
  const [run, setRun] = useState(r.valid ? r.query : "");
  const [name, setName] = useState(r.question.length > 80 ? `${r.question.slice(0, 79)}…` : r.question);
  const found = useQuery({ queryKey: ["ai-graph-run", run], queryFn: () => api.search(run, undefined, 25), enabled: !!run, retry: false });
  const save = useMutation({
    mutationFn: () => viewsApi.create({ page: "domande", name: name.trim(), kind: "table", query: run, settings: {}, scope: "private" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["views"] }),
  });
  const items = found.data?.items ?? [];
  return (
    <div className="mr-4 flex flex-col gap-3" data-testid="risposta-grafo">
      <span className="label">{t("ai.panel.assistant")}</span>
      {r.explanation && <p className="m-0 [overflow-wrap:anywhere]">{r.explanation}</p>}
      {r.corrected && r.valid && (
        <p className="m-0 text-[12.5px] text-muted" role="note">
          {t("ai.graph.corrected", { query: r.first_query ?? "", error: r.first_error?.message ?? "" })}
        </p>
      )}
      {!r.valid && (
        <p className="m-0 text-[12.5px] font-semibold text-danger" role="alert">
          {t("ai.graph.invalid", { error: r.error?.message ?? "" })}
        </p>
      )}
      <form
        className="flex flex-col gap-1"
        onSubmit={(e) => {
          e.preventDefault();
          setRun(query.trim());
          save.reset();
        }}
      >
        <label className="flex flex-col gap-1">
          <span className="label">{t("ai.graph.query")}</span>
          <textarea className="field min-h-[56px] font-mono text-[13px]" value={query} maxLength={1000} onChange={(e) => setQuery(e.target.value)} data-testid="query-grafo" />
        </label>
        <div className="flex flex-wrap items-center gap-2">
          <button type="submit" className="btn py-1" disabled={!query.trim()}>{t("ai.graph.run")}</button>
          {run && (
            <Link to="/elenco/$lista" params={{ lista: "domande" }} search={{ q: run }} className="text-[12.5px]">
              {t("ai.graph.openList")}
            </Link>
          )}
        </div>
      </form>
      {run && found.isPending && <Loading rows={2} />}
      <ErrorNote error={found.error} />
      {run && found.data && (
        <>
          <span className="text-[12.5px] text-muted" role="status">
            {items.length === 0 ? t("ai.graph.none") : t("ai.graph.count", { count: items.length, more: found.data.next ? "+" : "" })}
          </span>
          {items.length > 0 && (
            <ul className="m-0 flex list-none flex-col gap-1 p-0" aria-label={t("ai.graph.results")}>
              {items.map((el) => (
                <li key={el.id} className="text-[13px] [overflow-wrap:anywhere]">
                  <Link to="/e/$key" params={{ key: el.key }}>
                    <span className="font-mono">{el.key}</span> {el.title}
                  </Link>
                  <span className="text-muted"> · {el.type_name} · {el.state}</span>
                </li>
              ))}
            </ul>
          )}
          <form
            className="flex flex-col gap-1 border-t border-hair pt-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (name.trim()) save.mutate();
            }}
          >
            <label className="flex flex-col gap-1">
              <span className="label">{t("ai.graph.viewName")}</span>
              <input className="field py-1" value={name} maxLength={120} onChange={(e) => setName(e.target.value)} />
            </label>
            <div className="flex flex-wrap items-center gap-2">
              <button type="submit" className="btn py-1" disabled={!name.trim() || save.isPending || save.isSuccess}>{t("ai.graph.save")}</button>
              {save.isSuccess && (
                <span className="text-[12.5px] text-ok" role="status">
                  {t("ai.graph.saved")}{" "}
                  <Link to="/elenco/$lista" params={{ lista: "domande" }} search={{ v: save.data.id }}>{t("ai.graph.openView")}</Link>
                </span>
              )}
            </div>
            <ErrorNote error={save.error} />
          </form>
        </>
      )}
    </div>
  );
}

type Draft = { text: string; tools: string[] };

export function AssistantPanel({ onClose }: { onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["ai-status"], queryFn: aiApi.status });
  const convs = useQuery({ queryKey: ["ai-conversations"], queryFn: aiApi.conversations });
  const [convId, setConvId] = useState<string>("");
  const conv = useQuery({ queryKey: ["ai-conversation", convId], queryFn: () => aiApi.conversation(convId), enabled: !!convId });
  const [question, setQuestion] = useState("");
  const [mode, setMode] = useState<"chat" | "graph">("chat");
  const [draft, setDraft] = useState<Draft | null>(null);
  const [graph, setGraph] = useState<{ question: string; result: GraphResult }[]>([]);
  const input = useRef<HTMLTextAreaElement>(null);
  const end = useRef<HTMLDivElement>(null);

  useEffect(() => {
    input.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const onEvent = (e: AskEvent) =>
    setDraft((d) => {
      const cur = d ?? { text: "", tools: [] };
      switch (e.type) {
        case "text":
          return { ...cur, text: cur.text + e.text };
        case "tool":
          return { text: "", tools: cur.tools.includes(e.tool) ? cur.tools : [...cur.tools, e.tool] };
        case "step":
          return { ...cur, text: "" };
      }
      return cur;
    });
  const ask = useMutation({
    mutationFn: (q: string) => {
      setDraft({ text: "", tools: [] });
      return askStream(q, convId || undefined, onEvent);
    },
    onSettled: () => setDraft(null),
    onSuccess: (res: AskResult) => {
      qc.setQueryData(["ai-status"], res.status);
      qc.setQueryData(["ai-conversation", res.conversation.id], (old: { messages: ChatMessage[] } | undefined) => ({
        conversation: res.conversation,
        messages: [...(convId === res.conversation.id ? (old?.messages ?? []) : []), ...res.messages],
      }));
      setConvId(res.conversation.id);
      setQuestion("");
      qc.invalidateQueries({ queryKey: ["ai-conversations"] });
    },
  });
  const askGraph = useMutation({
    mutationFn: (q: string) => aiApi.graph(q),
    onSuccess: (res, q) => {
      qc.setQueryData(["ai-status"], res.status);
      setGraph((g) => [...g, { question: q, result: res }]);
      setQuestion("");
    },
  });
  const remove = useMutation({
    mutationFn: () => aiApi.deleteConversation(convId),
    onSuccess: () => {
      setConvId("");
      qc.invalidateQueries({ queryKey: ["ai-conversations"] });
    },
  });
  const messages = convId ? (conv.data?.messages ?? []) : [];
  useEffect(() => {
    // Graffe: in Chrome recenti scrollIntoView restituisce una Promise, che React prenderebbe per la pulizia.
    end.current?.scrollIntoView({ block: "end" });
  }, [messages.length, graph.length, mode]);

  const onProposal = (p: Proposal) => {
    qc.setQueryData(["ai-conversation", convId], (old: { conversation: unknown; messages: ChatMessage[] } | undefined) =>
      old ? { ...old, messages: old.messages.map((m) => ({ ...m, proposals: m.proposals.map((x) => (x.id === p.id ? p : x)) })) } : old,
    );
  };

  const st = status.data;
  const blocked = st?.budget.state === "blocked";
  const pending = ask.isPending || askGraph.isPending;
  const submit = () => {
    const q = question.trim();
    if (!q || pending) return;
    if (mode === "graph") askGraph.mutate(q);
    else ask.mutate(q);
  };

  return (
    <aside
      role="dialog"
      aria-modal="false"
      aria-labelledby="assistente-titolo"
      className="fixed inset-y-0 right-0 z-[60] flex w-full flex-col border-l-2 border-rule bg-bg shadow-[-6px_0_0_var(--m-platform)] md:w-[460px]"
      data-testid="assistente"
    >
      <div className="flex items-center gap-2 border-b border-rule px-4 py-3">
        <h2 id="assistente-titolo" className="m-0 flex-1 text-[18px] font-bold">{t("ai.panel.title")}</h2>
        <button className="btn py-1" onClick={onClose}>{t("ai.panel.close")}</button>
      </div>
      <div className="flex flex-col gap-2 border-b border-hair px-4 py-2 text-[12.5px] text-muted">
        <div className="flex flex-wrap gap-1" role="radiogroup" aria-label={t("ai.graph.mode")}>
          {(["chat", "graph"] as const).map((m) => (
            <button
              key={m}
              type="button"
              role="radio"
              aria-checked={mode === m}
              className={`btn py-1 ${mode === m ? "btn-primary" : ""}`}
              onClick={() => {
                setMode(m);
                input.current?.focus();
              }}
            >
              {t(m === "chat" ? "ai.graph.modeChat" : "ai.graph.modeGraph")}
            </button>
          ))}
        </div>
        {mode === "chat" && (
        <div className="flex flex-wrap items-center gap-2">
          <Select
            aria-label={t("ai.panel.conversations")}
            className="field min-w-0 flex-1 py-1"
            value={convId}
            onChange={(e) => setConvId(e.target.value)}
          >
            <option value="">{t("ai.panel.newConversation")}</option>
            {(convs.data?.items ?? []).map((c) => (
              <option key={c.id} value={c.id}>{c.title}</option>
            ))}
          </Select>
          {convId && (
            <button className="btn py-1" disabled={remove.isPending} onClick={() => remove.mutate()}>
              {t("ai.panel.delete")}
            </button>
          )}
        </div>
        )}
        {st && (
          <span>
            {t("ai.panel.info", { model: st.model ?? "—", days: st.retention_days })}
            {st.masked ? ` ${t("ai.panel.masked")}` : ""}
          </span>
        )}
        {st && !st.tools && <span className="text-fg" role="note">{t("ai.panel.noTools")}</span>}
        {st?.budget.state === "warn" && <span className="text-fg" role="note">{t("ai.panel.budgetWarn", { percent: fmtNumber(Math.floor(st.budget.percent)) })}</span>}
        {blocked && <span className="font-semibold text-danger" role="note">{t("ai.panel.budgetBlocked")}</span>}
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 py-4" role="log" aria-live="polite" aria-label={t("ai.panel.log")}>
        {mode === "chat" && (
          <>
            {convId && conv.isPending && <Loading rows={2} />}
            <ErrorNote error={conv.error} />
            {messages.length === 0 && !conv.isPending && !draft && <p className="m-0 text-muted">{t("ai.panel.empty")}</p>}
            {messages.map((m) => (
              <Message key={m.id} m={m} onProposal={onProposal} />
            ))}
            {ask.isPending && draft?.text && (
              <div className="mr-4 flex flex-col gap-2" data-testid="risposta-in-arrivo" aria-busy="true">
                <span className="label">{t("ai.panel.assistant")}</span>
                <p className="m-0 whitespace-pre-wrap [overflow-wrap:anywhere]">{draft.text}</p>
              </div>
            )}
            {ask.isPending && !draft?.text && (
              <p className="m-0 text-muted" role="status">
                {draft?.tools.length ? t("ai.panel.usingTools", { tools: draft.tools.map((n) => lookup(`ai.tool.${n}`) ?? n).join(", ") }) : t("ai.panel.thinking")}
              </p>
            )}
          </>
        )}
        {mode === "graph" && (
          <>
            {graph.length === 0 && <p className="m-0 text-muted">{t("ai.graph.empty")}</p>}
            {graph.map((g, i) => (
              <div key={i} className="flex flex-col gap-3">
                <div className="ml-8 flex flex-col gap-1 self-end border border-hair bg-side px-3 py-2" data-testid="domanda">
                  <span className="sr-only">{t("ai.panel.you")}</span>
                  <p className="m-0 whitespace-pre-wrap [overflow-wrap:anywhere]">{g.question}</p>
                </div>
                <GraphAnswer r={g.result} />
              </div>
            ))}
            {askGraph.isPending && <p className="m-0 text-muted" role="status">{t("ai.graph.thinking")}</p>}
          </>
        )}
        <div ref={end} />
      </div>
      <form
        className="flex flex-col gap-2 border-t border-rule px-4 py-3"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <ErrorNote error={mode === "graph" ? askGraph.error : ask.error} />
        <label className="flex flex-col gap-1">
          <span className="label">{t(mode === "graph" ? "ai.graph.question" : "ai.panel.question")}</span>
          <textarea
            ref={input}
            className="field min-h-[72px]"
            maxLength={4000}
            value={question}
            disabled={blocked}
            placeholder={t(mode === "graph" ? "ai.graph.placeholder" : "ai.panel.placeholder")}
            onChange={(e) => setQuestion(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                submit();
              }
            }}
          />
        </label>
        <div className="flex items-center gap-2">
          <button type="submit" className="btn btn-primary" disabled={!question.trim() || pending || blocked}>
            {pending ? t("ai.panel.asking") : t("ai.panel.ask")}
          </button>
          <span className="text-[12px] text-muted">{t("ai.panel.hint")}</span>
        </div>
      </form>
    </aside>
  );
}
