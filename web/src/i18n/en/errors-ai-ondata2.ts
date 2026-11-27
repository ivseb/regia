import type { Translation } from "../types";
import type it from "../it/errors-ai-ondata2";

export default {
  "errors.ai.mcp_disabled": "The MCP server is not turned on in this organization: an administrator turns it on in Configuration → Artificial intelligence.",
  "errors.ai.stream_interrupted": "The answer stopped before the end: try again.",
  "errors.auth.token_required": "An API token is needed: Authorization: Bearer followed by the token.",
} satisfies Translation<typeof it>;
