import type { Translation } from "../types";
import type it from "../it/errors-board";

export default {
  "errors.validation.board.type_lacks_phase": "An item of type {type} has no phase in the column “{column}”: assign the column a phase of its process or move it by hand.",
  "errors.validation.board.type_unknown": "Column “{name}”: the type {type} is not on the board.",
  "errors.validation.board.sort_invalid": "The board can be sorted manually or by due date.",
  "errors.validation.board.no_draft": "There is no draft to publish: edit the columns first.",
  "errors.validation.board.no_columns": "The board needs at least one column.",
  "errors.validation.board.page_needs_all": "Per-column pages only apply when the board shows the whole space, without a sprint.",
  "errors.validation.board.cursor": "The card to continue from is no longer in this column: reload the board.",
} satisfies Translation<typeof it>;
