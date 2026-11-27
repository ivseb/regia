import type { Translation } from "../types";
import type it from "../it/errors-ai-wiki";

export default {
  "errors.wiki.not_found": "Page not found: it does not exist, was removed or belongs to someone else.",
  "errors.wiki.conflict": "The page changed in the meantime (maybe the assistant updated it). Reload it and try again.",
  "errors.wiki.not_owner": "The page belongs to someone else: you can only read it.",
  "errors.wiki.title_invalid": "Write a title (up to 120 characters).",
  "errors.wiki.title_taken": "You already have a page with this title.",
  "errors.wiki.body_invalid": "Text too long: up to 50,000 characters.",
  "errors.wiki.too_many": "The wiki already has 200 pages. Remove some.",
  "errors.wiki.version_invalid": "This version does not exist or is no longer kept.",
  "errors.wiki.undo_not_latest": "You can only undo the latest change of a page. For older ones, restore a version from the history.",
  "errors.wiki.add_invalid": "Choose an item or a text to add.",
  "errors.wiki.text_invalid": "Text too long: up to 8,000 characters.",
  "errors.wiki.space_required": "Choose the Knowledge space.",
  "errors.wiki.no_member": "The wiki belongs to people who work in the organization.",
  "errors.ai.wiki_running": "The wiki update is already running.",
  "errors.ai.wiki_no_tools": "The wiki model cannot use tools. An organization admin must choose another one.",
} satisfies Translation<typeof it>;
