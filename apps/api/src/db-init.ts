import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { sql } from "./repository.js";

const here = dirname(fileURLToPath(import.meta.url));
const schema = await readFile(join(here, "schema.sql"), "utf8");

try {
  await sql.unsafe(schema);
  console.log("TraceForge database is ready.");
} finally {
  await sql.end({ timeout: 5 });
}
