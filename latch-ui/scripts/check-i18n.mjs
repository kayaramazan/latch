// Fails the build when the Turkish dictionary is missing a key present in
// English (or vice versa). ROADMAP DoD: missing key must be a CI error.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const en = JSON.parse(readFileSync(join(here, "../src/i18n/en.json"), "utf8"));
const tr = JSON.parse(readFileSync(join(here, "../src/i18n/tr.json"), "utf8"));

const enKeys = new Set(Object.keys(en));
const trKeys = new Set(Object.keys(tr));
const missingInTr = [...enKeys].filter((k) => !trKeys.has(k));
const missingInEn = [...trKeys].filter((k) => !enKeys.has(k));

let ok = true;
if (missingInTr.length) { console.error("Missing in tr.json:", missingInTr); ok = false; }
if (missingInEn.length) { console.error("Missing in en.json:", missingInEn); ok = false; }
const emptyTr = [...trKeys].filter((k) => !tr[k]?.trim());
if (emptyTr.length) { console.error("Empty Turkish values:", emptyTr); ok = false; }

if (!ok) process.exit(1);
console.log(`i18n OK: ${enKeys.size} keys, en + tr complete.`);
