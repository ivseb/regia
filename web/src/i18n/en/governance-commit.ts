import type { Translation } from "../types";
import type it from "../it/governance-commit";

export default {
  "governance.dora.primo_commit": "first commit linked to an item",
  "governance.dora.primo_commit_2": "First commit linked to an item",
  "governance.dora.il_primo_commit": "The first commit that cites an item of the service after the previous release; if there is none, the first check on the released commit, then the change creation.",
  "governance.dora.il_primo_commit_solo": "The first commit that cites an item of the service (a key like HL-12 in the message) after the previous release, read by the GitHub and Gitea connectors.",
} satisfies Translation<typeof it>;
