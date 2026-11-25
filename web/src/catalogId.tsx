import { tr } from "./i18n/core";
// Identificatore di un elemento di architettura: il codice di catalogo (APP-MOB) in evidenza e
// la chiave dell'elemento (ARC-8) piccola accanto. Il codice, se c'è, vale come la chiave in
// link e ricerca (/e/APP-MOB, ⌘K). Senza codice resta la sola chiave.
export function CatalogId({ code, itemKey, className = "" }: { code?: string | null; itemKey: string; className?: string }) {
  if (!code) return <span className={`font-mono ${className}`}>{itemKey}</span>;
  return (
    <span className={`font-mono ${className}`}>
      <span className="font-semibold">{code}</span>
      <span className="ml-1.5 text-[10.5px] font-normal text-muted" title={tr("governance.catalogid.chiave_dell_elemento_vale_come")}>
        {itemKey}
      </span>
    </span>
  );
}
