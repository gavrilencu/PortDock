// Checks that every t("…") / tk("…") / tx("…") text in src/ has a Romanian and a Russian translation.
// Usage: node scripts/i18n-check.mjs [file-or-dir…]   (default: src)
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const src = join(root, "src");

const ro = {};
const ru = {};
for (const f of readdirSync(join(src, "locales"))) {
  if (!f.endsWith(".ts")) continue;
  const m = await import(pathToFileURL(join(src, "locales", f)).href);
  for (const [lang, d] of [["ro", ro], ["ru", ru]]) {
    for (const [k, v] of Object.entries(m[lang] ?? {})) {
      if (k in d && d[k] !== v) console.warn(`conflicting ${lang} for ${JSON.stringify(k)} in ${f}`);
      d[k] = v;
    }
  }
}

const walk = (p) =>
  statSync(p).isDirectory() ? readdirSync(p).flatMap((f) => (f === "locales" ? [] : walk(join(p, f)))) : /\.tsx?$/.test(p) ? [p] : [];
const args = process.argv.slice(2);
const targets = (args.length ? args : [src]).flatMap((p) => walk(p));

const call = /\bt[kx]?\(\s*("(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'|`(?:[^`\\$]|\\.)*`)/g;
const dynamic = /\bt\(\s*(?![\s"'`)])/g;
const placeholders = (s) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join(",");

let missing = 0;
let total = 0;
for (const file of targets) {
  if (file.endsWith("i18n.tsx")) continue;
  const text = readFileSync(file, "utf8");
  const rel = relative(root, file).replaceAll("\\", "/");
  for (const m of text.matchAll(call)) {
    const key = Function(`return ${m[1]}`)();
    total++;
    for (const [lang, d] of [["ro", ro], ["ru", ru]]) {
      if (!(key in d)) {
        missing++;
        console.log(`${rel}: missing ${lang}: ${JSON.stringify(key)}`);
      } else if (d[key].split("|").some((form) => placeholders(form) !== placeholders(key.split("|")[0]))) {
        console.log(`${rel}: placeholder mismatch in ${lang}: ${JSON.stringify(key)} -> ${JSON.stringify(d[key])}`);
      }
    }
  }
  for (const m of text.matchAll(dynamic)) {
    const line = text.slice(0, m.index).split("\n").length;
    console.log(`${rel}:${line}: t() with a non-literal text (not checked): ${text.slice(m.index, m.index + 60).split("\n")[0]}`);
  }
}
console.log(`\n${total} texts checked, ${missing} missing translations.`);
process.exit(missing ? 1 : 0);
