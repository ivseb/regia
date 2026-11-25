import type { Translation } from "../types";
import type it from "../it/shell-prova";

export default {
  "shell.prova.titolo": "Test environment",
  "shell.prova.testo": "You are working on a copy: the real data does not change.",
  "shell.prova.promuovi": "Promote to the main one",
  "shell.prova.torna": "Back to the main one",
} satisfies Translation<typeof it>;
