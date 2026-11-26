// Modello di partenza «Persona sola / home lab» (area «configurazione»): scheda del modello in «Inizia da qui».
// Le chiavi iniziano con «configurazione.»: vedi docs/sviluppo/testi-e-lingue.md.
import type { Source } from "../types";

export default {
  "configurazione.avvio.modello.persona_sola.nome": "Persona sola / home lab",
  "configurazione.avvio.modello.persona_sola.testo": "Per lavorare da soli: attività, bug e idee su una board kanban, con un flusso semplice (Da fare, In corso, Fatto). Accende lavoro, DevSecOps, Knowledge e architettura; spegne domanda e conformità.",
  "configurazione.avvio.kind.field": "Campi",
  "configurazione.avvio.kind.team": "Team",
  "configurazione.avvio.kind.board": "Board",
  "configurazione.persona.chiave": "Chiave della board",
  "configurazione.persona.chiaveAiuto": "Lettere maiuscole e cifre, inizia con una lettera (per esempio HL). Fa parte del codice di ogni elemento, come HL-1.",
  "configurazione.persona.team": "Nome del team",
  "configurazione.persona.teamAiuto": "Il team che lavora sulla board: tu sei il referente e l'unico membro, gli altri si aggiungono dopo.",
  "configurazione.persona.teamPredefinito": "Home lab",
  "configurazione.persona.spento": "spento",
  "configurazione.persona.archiviato": "archiviato",
  "configurazione.persona.fattoVaiBoard": "Apri la board",
  "configurazione.persona.nota": "Story, epic e sprint si archiviano solo se non hanno elementi. Il flusso nuovo si pubblica come nuova versione del processo di ciascun tipo: niente si cancella.",
} satisfies Source;
