// Element history: keyword closing from commits (area "lavoro").
import type { Translation } from "../types";
import type it from "../it/lavoro-rilasci";

export default {
  "lavoro.elementsections.h.commitClosed": "closed by commit {sha} ({keyword}): phase {to}",
  "lavoro.elementsections.h.commitCloseSkipped": "commit {sha} asks to close it ({keyword}), but the process does not allow moving to the final phase",
  "lavoro.elementsections.h.commitCloseAmbiguous": "commit {sha} asks to close it ({keyword}), but more than one final phase is possible: choose by hand",
} satisfies Translation<typeof it>;
