import type { Translation } from "../types";
import type it from "../it/ai-ragionamento";

export default {
  "ai.cfg.reasoning": "Reasoning",
  "ai.cfg.reasoningHelp": "How much the model reasons before answering: more means more accurate answers, but slower and more expensive. On automatic, the fast tier reasons little and the deep tier a lot (the assistant, if moved to the deep tier, medium, since it takes several rounds with tools); short requests (graph questions, summaries, classification) always reason little, unless the feature has its own value. Ollama and compatible services decide for themselves.",
  "ai.cfg.reasoning.auto": "Automatic",
  "ai.cfg.reasoning.low": "Low",
  "ai.cfg.reasoning.medium": "Medium",
  "ai.cfg.reasoning.high": "High",
  "ai.cfg.reasoningTier": "Same as the tier",
  "ai.cfg.reasoningFor": "Reasoning for {name}",
  "ai.reason.max_tokens": "The model ran out of tokens before answering: try again or simplify the question.",
  "ai.reason.empty": "The model answered with no text.",
} satisfies Translation<typeof it>;
