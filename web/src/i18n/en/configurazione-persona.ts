import type { Translation } from "../types";
import type it from "../it/configurazione-persona";

export default {
  "configurazione.avvio.modello.persona_sola.nome": "Solo person / home lab",
  "configurazione.avvio.modello.persona_sola.testo": "For working alone: tasks, bugs and ideas on a kanban board, with a simple flow (To do, In progress, Done). Turns on work, DevSecOps, Knowledge and architecture; turns off demand and compliance.",
  "configurazione.avvio.kind.field": "Fields",
  "configurazione.avvio.kind.team": "Team",
  "configurazione.avvio.kind.board": "Board",
  "configurazione.persona.chiave": "Board key",
  "configurazione.persona.chiaveAiuto": "Capital letters and digits, starting with a letter (for example HL). It is part of every item's code, like HL-1.",
  "configurazione.persona.team": "Team name",
  "configurazione.persona.teamAiuto": "The team working on the board: you are its lead and only member, others can be added later.",
  "configurazione.persona.teamPredefinito": "Home lab",
  "configurazione.persona.spento": "turned off",
  "configurazione.persona.archiviato": "archived",
  "configurazione.persona.fattoVaiBoard": "Open the board",
  "configurazione.persona.nota": "Story, epic and sprint are archived only if they have no items. The new flow is published as a new version of each type's process: nothing is deleted.",
} satisfies Translation<typeof it>;
