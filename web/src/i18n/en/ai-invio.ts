// Assistant panel: sending the question (straight into the conversation) and progress.
import type { Translation } from "../types";
import type it from "../it/ai-invio";

export default {
  "ai.panel.step.think": "Working out what to look for…",
  "ai.panel.step.read": "Used {tools}: preparing the answer…",
  "ai.panel.step.write": "Writing the answer…",
  "ai.panel.elapsed": "{seconds} s",
  "ai.panel.restored": "The question was not sent: it is back in the box, you can try again.",
} satisfies Translation<typeof it>;
