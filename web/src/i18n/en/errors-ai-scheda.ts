import type { Translation } from "../types";
import type it from "../it/errors-ai-scheda";

export default {
  "errors.ai.budget_insufficient": "The monthly AI budget is not enough for this request: try with less text or wait for next month. Everything else works as usual.",
  "errors.ai.card_action_invalid": "Unknown assistant action.",
  "errors.ai.draft_invalid": "Unknown draft type: comment, reply or description.",
  "errors.ai.draft_empty": "The draft is empty.",
  "errors.ai.hint_invalid": "Instructions too long: 500 characters at most.",
  "errors.ai.links_invalid": "Give between one and ten links.",
  "errors.ai.title_invalid": "Write a title of at least 3 characters.",
  "errors.ai.type_invalid": "Unknown item type.",
  "errors.ai.proposal_invalid": "It cannot be added: the item does not exist, is not visible or you cannot edit it.",
} satisfies Translation<typeof it>;
