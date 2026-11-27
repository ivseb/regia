// AI, wave 2 (replica/ai.md): MCP server, graph questions, streaming, budget notice.
import type { Translation } from "../types";
import type it from "../it/ai-ondata2";

export default {
  "ai.feature.mcp": "MCP server",
  "ai.tool.conferma": "confirm",
  "ai.tool.rifiuta": "reject",
  "ai.tool.proposte": "proposals",

  "ai.notif.label": "AI",
  "ai.notif.hint": "Monthly AI budget above 80% (to the organization's administrators)",
  "ai.notif.budget_warning": "The AI budget for {month} is <f>at {percent}%</f>: at 100% the AI features stop until next month",
  "ai.notif.plain": "AI notice",

  "ai.mcp.title": "MCP server",
  "ai.mcp.intro": "With the MCP server, Claude Desktop, Claude Code or an IDE work on Regia with the permissions of the person who connects. The model is that person's: Regia calls no provider, counts no tokens and cannot mask personal data, because the client reads it. Changes stay proposals, which the person confirms.",
  "ai.mcp.enable": "MCP server on",
  "ai.mcp.enableHelp": "Independent of Regia's AI: no provider needed. Each person connects with their own API token, created in their profile; with a read-only token they only see the read tools.",
  "ai.mcp.url": "Address for the client",
  "ai.mcp.urlHelp": "With the Authorization: Bearer header followed by the token. The administrators' guide has examples for Claude Desktop, Claude Code and IDEs.",

  "ai.panel.usingTools": "Using: {tools}…",

  "ai.graph.path": "graph questions",
  "ai.graph.listTitle": "Graph questions",
  "ai.graph.listEmpty": "No items match this query.",
  "ai.graph.mode": "What you are asking",
  "ai.graph.modeChat": "Conversation",
  "ai.graph.modeGraph": "Graph question",
  "ai.graph.empty": "Ask which items you want to see, for example “which open requests are urgent?”. The assistant writes the Regia query: you see it, can change it and save it as a view.",
  "ai.graph.thinking": "Writing the query…",
  "ai.graph.question": "Your graph question",
  "ai.graph.placeholder": "For example: which critical applications have overdue vulnerabilities?",
  "ai.graph.query": "Query",
  "ai.graph.run": "Run",
  "ai.graph.openList": "Open in the list",
  "ai.graph.none": "No items found.",
  "ai.graph.count": { one: "{count}{more} item", other: "{count}{more} items" },
  "ai.graph.results": "Items found",
  "ai.graph.viewName": "View name",
  "ai.graph.save": "Save as view",
  "ai.graph.saved": "View saved.",
  "ai.graph.openView": "Open it",
  "ai.graph.corrected": "The first query (“{query}”) was not valid: {error}. The assistant corrected it once.",
  "ai.graph.invalid": "The assistant could not write a valid query, even after correcting it: {error}. Fix it by hand below.",
} satisfies Translation<typeof it>;
