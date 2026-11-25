// Modelli CSV scaricabili per gli import (applicazioni, costi, lavoro, organizzazione): le
// intestazioni sono quelle che l'import riconosce, con una riga d'esempio. Punto e virgola e
// BOM, così Excel lo apre bene. Funzioni pure per il testo; il download è nel browser.
import { tr } from "../i18n/core.ts";

export type CsvKind = "lavoro" | "applicazioni" | "costi" | "organizzazione";

export type CsvModel = { file: string; header: string[]; rows: string[][] };

/** Modello di ogni import: nome del file, intestazioni riconosciute e una riga d'esempio. */
export function csvModel(kind: CsvKind): CsvModel {
  switch (kind) {
    case "applicazioni":
      return {
        file: "modello-applicazioni.csv",
        // i18n-ok: intestazioni del file, quelle che l'import riconosce
        header: ["codice", "nome", "descrizione", "criticita", "funzione_critica", "classificazione_dati", "hosting", "rto_minuti", "rpo_minuti", "team", "asset"],
        rows: [["APP-1", tr("casa.import.sampleApp"), "", "alta", tr("casa.import.yes"), "riservati", "", "240", "60", tr("casa.import.sampleTeam"), ""]],
      };
    case "costi":
      return {
        file: "modello-costi.csv",
        // i18n-ok: intestazioni del file
        header: ["elemento", "data", "importo", "valuta", "categoria", "tipo", "riferimento", "descrizione", "fornitore", "centro_di_costo", "capitalizzabile"],
        rows: [["DEM-12", "2026-03-31", "12500,00", "EUR", tr("casa.import.sampleCategory"), "consuntivo", "FT-2026-0142", tr("casa.import.sampleCost"), "", "", tr("casa.import.no")]],
      };
    case "organizzazione":
      return {
        file: "modello-organizzazione.csv",
        // i18n-ok: intestazioni del file
        header: ["nome", "codice", "padre", "tipo", "responsabile", "referente"],
        rows: [[tr("casa.import.sampleUnit"), "AREA-1", "", "area", "", ""]],
      };
    case "lavoro":
      return {
        file: "modello-lavoro.csv",
        // i18n-ok: intestazioni del file; nella mappatura si assegna un ruolo a ogni colonna
        header: ["chiave", "titolo", "tipo", "stato", "genitore", "assegnatario", "scadenza", "etichette", "ore_stimate"],
        rows: [["T-1", tr("casa.import.sampleTask"), "task", "da fare", "", "", "2026-06-30", "", "8"]],
      };
  }
}

const cell = (v: string) => (/[;"\n\r]/.test(v) ? `"${v.replaceAll('"', '""')}"` : v);

/** Il testo del file: punto e virgola, righe CRLF, BOM per Excel. */
export function csvText(m: CsvModel): string {
  return "﻿" + [m.header, ...m.rows].map((r) => r.map(cell).join(";")).join("\r\n") + "\r\n";
}

/** Avvia il download del modello nel browser. */
export function downloadCsv(kind: CsvKind): void {
  const m = csvModel(kind);
  const url = URL.createObjectURL(new Blob([csvText(m)], { type: "text/csv;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = m.file;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
