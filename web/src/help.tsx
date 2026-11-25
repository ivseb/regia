// Spiegazione dei termini: `<Term k="impatto">impatto</Term>` sottolinea la parola e, al passaggio del
// mouse, con il focus da tastiera o al tocco, mostra una frase semplice (glossario in i18n/it/help.ts).
// Accessibile: la parola è un pulsante raggiungibile con Tab, il testo è collegato con aria-describedby,
// Esc lo chiude. Il riquadro è `fixed` e si tiene dentro lo schermo (nessuno scroll orizzontale sul telefono).
import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useT } from "./i18n/react";

export const TERMS = ["richiesta", "fase", "valutazione", "impatto", "epic", "story", "approvazione", "regola", "processo", "versione"] as const;
export type TermKey = (typeof TERMS)[number];

export function Term({ k, children }: { k: TermKey; children: ReactNode }) {
  const t = useT();
  const id = useId();
  const btn = useRef<HTMLButtonElement>(null);
  const tip = useRef<HTMLSpanElement>(null);
  const [open, setOpen] = useState(false);
  const [pinned, setPinned] = useState(false); // aperto dal tocco o dal clic: resta finché non si chiude
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);

  const close = useCallback(() => {
    setOpen(false);
    setPinned(false);
  }, []);

  // Posizione: sotto la parola, dentro i margini dello schermo; sopra se sotto non c'è spazio.
  useLayoutEffect(() => {
    if (!open || !btn.current || !tip.current) return;
    const b = btn.current.getBoundingClientRect();
    const w = tip.current.offsetWidth;
    const h = tip.current.offsetHeight;
    const vw = document.documentElement.clientWidth;
    const vh = window.innerHeight;
    const left = Math.max(8, Math.min(b.left, vw - w - 8));
    const below = b.bottom + 6;
    const top = below + h > vh - 8 && b.top - h - 6 > 8 ? b.top - h - 6 : below;
    setPos({ left, top });
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    const onDown = (e: Event) => {
      if (!pinned) return;
      const n = e.target as Node;
      if (!btn.current?.contains(n) && !tip.current?.contains(n)) close();
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onDown);
    window.addEventListener("scroll", close, true);
    window.addEventListener("resize", close);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onDown);
      window.removeEventListener("scroll", close, true);
      window.removeEventListener("resize", close);
    };
  }, [open, pinned, close]);

  return (
    <>
      <button
        ref={btn}
        type="button"
        className="m-0 inline cursor-help border-0 border-b border-dotted border-muted bg-transparent p-0 text-inherit [font:inherit] hover:border-fg"
        aria-describedby={open ? id : undefined}
        aria-expanded={open}
        data-term={k}
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => !pinned && setOpen(false)}
        onFocus={() => setOpen(true)}
        onBlur={() => !pinned && setOpen(false)}
        onClick={(e) => {
          e.preventDefault();
          if (open && pinned) close();
          else {
            setOpen(true);
            setPinned(true);
          }
        }}
      >
        {children}
      </button>
      {open &&
        createPortal(
          <span
            ref={tip}
            id={id}
            role="tooltip"
            style={{ position: "fixed", left: pos?.left ?? 0, top: pos?.top ?? 0, visibility: pos ? "visible" : "hidden" }}
            className="z-50 block w-[min(18rem,calc(100vw-16px))] border border-rule bg-bg px-3 py-2 text-left text-[12.5px] font-normal normal-case leading-snug tracking-normal text-fg shadow-sm"
          >
            {t(`help.term.${k}`)}
          </span>,
          document.body,
        )}
    </>
  );
}
