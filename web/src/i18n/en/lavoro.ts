import type { Translation } from "../types";
import type it from "../it/lavoro";
import areas from "./lavoro/areas.gen.ts";

export default areas satisfies Translation<typeof it>;
