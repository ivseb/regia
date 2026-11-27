// Pannello «Assistente» (replica/ai.md, ondata 1: primo uso). La persona fa una domanda,
// l'assistente legge con gli strumenti e con i permessi della persona e risponde con i link
// agli elementi; le scritture arrivano come proposte da confermare o rifiutare qui. Compare
// solo con l'AI accesa e un modello configurato: senza, il resto dell'app non cambia.
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { aiApi, type AskResult, type ChatMessage, type Proposal, type Ref } from "../aiApi";
import { Select } from "../select";
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
    </div>
  );
}

export function AssistantPanel({ onClose }: { onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["ai-status"], queryFn: aiApi.status });
  const convs = useQuery({ queryKey: ["ai-conversations"], queryFn: aiApi.conversations });
  const [convId, setConvId] = useState<string>("");
  const conv = useQuery({ queryKey: ["ai-conversation", convId], queryFn: () => aiApi.conversation(convId), enabled: !!convId });
  const [question, setQuestion] = useState("");
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

  const ask = useMutation({
    mutationFn: (q: string) => aiApi.ask(q, convId || undefined),
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
  const remove = useMutation({
    mutationFn: () => aiApi.deleteConversation(convId),
    onSuccess: () => {
      setConvId("");
      qc.invalidateQueries({ queryKey: ["ai-conversations"] });
    },
  });
  const messages = convId ? (conv.data?.messages ?? []) : [];
  useEffect(() => end.current?.scrollIntoView({ block: "end" }), [messages.length]);

  const onProposal = (p: Proposal) => {
    qc.setQueryData(["ai-conversation", convId], (old: { conversation: unknown; messages: ChatMessage[] } | undefined) =>
      old ? { ...old, messages: old.messages.map((m) => ({ ...m, proposals: m.proposals.map((x) => (x.id === p.id ? p : x)) })) } : old,
    );
  };

  const st = status.data;
  const blocked = st?.budget.state === "blocked";
  const submit = () => {
    const q = question.trim();
    if (q && !ask.isPending) ask.mutate(q);
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
        {convId && conv.isPending && <Loading rows={2} />}
        <ErrorNote error={conv.error} />
        {messages.length === 0 && !conv.isPending && <p className="m-0 text-muted">{t("ai.panel.empty")}</p>}
        {messages.map((m) => (
          <Message key={m.id} m={m} onProposal={onProposal} />
        ))}
        {ask.isPending && <p className="m-0 text-muted" role="status">{t("ai.panel.thinking")}</p>}
        <div ref={end} />
      </div>
      <form
        className="flex flex-col gap-2 border-t border-rule px-4 py-3"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <ErrorNote error={ask.error} />
        <label className="flex flex-col gap-1">
          <span className="label">{t("ai.panel.question")}</span>
          <textarea
            ref={input}
            className="field min-h-[72px]"
            maxLength={4000}
            value={question}
            disabled={blocked}
            placeholder={t("ai.panel.placeholder")}
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
          <button type="submit" className="btn btn-primary" disabled={!question.trim() || ask.isPending || blocked}>
            {ask.isPending ? t("ai.panel.asking") : t("ai.panel.ask")}
          </button>
          <span className="text-[12px] text-muted">{t("ai.panel.hint")}</span>
        </div>
      </form>
    </aside>
  );
}
