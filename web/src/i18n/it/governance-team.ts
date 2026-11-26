// Creazione del team in Architettura → Team (pilota home lab, difetto 7): chiave e modello della board.
// Le chiavi iniziano con «governance.»: vedi docs/sviluppo/testi-e-lingue.md.
import type { Source } from "../types";

export default {
  "governance.team.chiave": "Chiave della board",
  "governance.team.chiaveAiuto": "La chiave fa parte del codice di ogni elemento, come LAB-1, e non si cambia dopo. Lettere e cifre, inizia con una lettera; vuota, si usa quella proposta dal nome.",
  "governance.team.modello": "Modello della board",
  "governance.team.modello.nessuno": "Una colonna per fase",
  "governance.team.modello.scrum": "A sprint (Scrum)",
  "governance.team.modello.kanban": "Kanban, senza sprint",
  "governance.team.modello.supporto": "Supporto, per scadenza",
} satisfies Source;
