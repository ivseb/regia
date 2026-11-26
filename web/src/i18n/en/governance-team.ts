import type { Translation } from "../types";
import type it from "../it/governance-team";

export default {
  "governance.team.chiave": "Board key",
  "governance.team.chiaveAiuto": "The key is part of every item's code, like LAB-1, and cannot be changed later. Letters and digits, starting with a letter; left empty, the one suggested from the name is used.",
  "governance.team.modello": "Board template",
  "governance.team.modello.nessuno": "One column per phase",
  "governance.team.modello.scrum": "Sprints (Scrum)",
  "governance.team.modello.kanban": "Kanban, no sprints",
  "governance.team.modello.supporto": "Support, by due date",
} satisfies Translation<typeof it>;
