// Safe Markdown for assistant replies. The text is parsed into React elements —
// never into HTML — so model output cannot inject markup or scripts. Links are
// limited to http(s) and mailto, images are shown as links (nothing is loaded),
// and unfinished syntax while a reply streams (an unclosed ``` fence, a
// half-typed table or list) renders as well as it can instead of failing.
import { Fragment, memo, useState, type ReactNode } from "react";
import { Check, Copy } from "lucide-react";
import { toast } from "sonner";
import { t } from "../../lib/i18n";
import { cn } from "../../lib/utils";

// ---------- Clipboard ----------

/** Copies text; falls back to a hidden textarea where the Clipboard API is unavailable (plain HTTP). */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (window.isSecureContext && navigator.clipboard) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* fall back below */
  }
  const focused = document.activeElement as HTMLElement | null;
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.cssText = "position:fixed;top:0;left:0;opacity:0;pointer-events:none";
  document.body.appendChild(ta);
  try {
    ta.select();
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    ta.remove();
    focused?.focus?.();
  }
}

/** Small copy button with inline feedback (no toast, so it doesn't cover the chat). */
export function CopyIconButton({ value, label, className }: { value: string; label?: string; className?: string }) {
  const [done, setDone] = useState(false);
  const title = label ?? t("Copy");
  return (
    <button
      type="button"
      title={title}
      aria-label={title}
      className={cn(
        "flex size-6 shrink-0 items-center justify-center rounded-md text-subtle transition-colors hover:bg-surface-2 hover:text-fg focus-visible:ring-[3px] focus-visible:ring-[var(--ring)] focus-visible:outline-none",
        className,
      )}
      onClick={async () => {
        if (await copyText(value)) {
          setDone(true);
          setTimeout(() => setDone(false), 1500);
        } else toast.error(t("Could not copy to the clipboard"));
      }}
    >
      {done ? <Check className="size-3.5 text-success" /> : <Copy className="size-3.5" />}
    </button>
  );
}

// ---------- Blocks ----------

type Align = "left" | "center" | "right" | null;
interface ListItem {
  /** null = not a task item, otherwise whether it is checked. */
  task: boolean | null;
  blocks: Block[];
}
type Block =
  | { kind: "paragraph"; text: string }
  | { kind: "heading"; level: number; text: string }
  | { kind: "code"; lang: string; code: string }
  | { kind: "rule" }
  | { kind: "quote"; blocks: Block[] }
  | { kind: "list"; ordered: boolean; start: number; items: ListItem[] }
  | { kind: "table"; align: Align[]; head: string[]; rows: string[][] };

const HEADING = /^ {0,3}(#{1,6})[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$/;
const RULE = /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/;
const QUOTE = /^ {0,3}> ?/;
const ITEM = /^([ \t]*)([-*+]|\d{1,9}[.)])(?:([ \t]+)(.*))?$/;
const TASK = /^\[([ xX])\](?:[ \t]+|$)/;
const DELIMITER = /^:?-+:?$/;
const FENCE = /^([ \t]*)(`{3,}|~{3,})(.*)$/;
const FENCE_END = /^[ \t]*(`{3,}|~{3,})[ \t]*$/;

interface Fence {
  indent: number;
  marker: string;
  lang: string;
}

const isBlank = (line: string) => line.trim() === "";

/** Width of leading whitespace (tabs stop every 4 columns). */
function columns(ws: string): number {
  let w = 0;
  for (const ch of ws) w = ch === "\t" ? w + 4 - (w % 4) : w + 1;
  return w;
}
const indentOf = (line: string) => columns(/^[ \t]*/.exec(line)![0]);

function dedent(line: string, n: number): string {
  let w = 0;
  let i = 0;
  while (i < line.length && w < n && (line[i] === " " || line[i] === "\t")) {
    w = line[i] === "\t" ? w + 4 - (w % 4) : w + 1;
    i++;
  }
  return line.slice(i);
}

function fenceAt(line: string): Fence | null {
  const m = FENCE.exec(line);
  if (!m) return null;
  const info = m[3].trim();
  if (m[2][0] === "`" && info.includes("`")) return null; // ```inline``` code, not a fence
  const lang = info.split(/\s+/)[0].replace(/[{}]/g, "").replace(/^\./, "").slice(0, 24);
  return { indent: columns(m[1]), marker: m[2], lang };
}

function closesFence(line: string, f: Fence): boolean {
  const m = FENCE_END.exec(line);
  return !!m && m[1][0] === f.marker[0] && m[1].length >= f.marker.length;
}

function splitRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|") && !s.endsWith("\\|")) s = s.slice(0, -1);
  const cells: string[] = [];
  let cell = "";
  for (let i = 0; i < s.length; i++) {
    if (s[i] === "\\" && s[i + 1] === "|") {
      cell += "|";
      i++;
    } else if (s[i] === "|") {
      cells.push(cell.trim());
      cell = "";
    } else cell += s[i];
  }
  cells.push(cell.trim());
  return cells;
}

/** A GitHub table starting at line i (header row + delimiter row), if any. */
function tableAt(lines: string[], i: number): { block: Block; next: number } | null {
  const head = lines[i];
  const delim = lines[i + 1];
  if (delim === undefined || !head.includes("|") || !delim.includes("|")) return null;
  const spec = splitRow(delim);
  if (!spec.every((c) => DELIMITER.test(c))) return null;
  const cols = splitRow(head);
  if (cols.length !== spec.length) return null;
  const align = spec.map<Align>((c) => (c.endsWith(":") ? (c.startsWith(":") ? "center" : "right") : c.startsWith(":") ? "left" : null));
  const rows: string[][] = [];
  let j = i + 2;
  for (; j < lines.length && !isBlank(lines[j]) && lines[j].includes("|"); j++) {
    const cells = splitRow(lines[j]);
    rows.push(cols.map((_, k) => cells[k] ?? ""));
  }
  return { block: { kind: "table", align, head: cols, rows }, next: j };
}

/** Whether line i starts a block that ends the paragraph before it. */
function startsBlock(lines: string[], i: number): boolean {
  const line = lines[i];
  if (fenceAt(line) || RULE.test(line) || QUOTE.test(line)) return true;
  if (HEADING.exec(line)?.[2]) return true;
  const item = ITEM.exec(line);
  if (item && item[4]?.trim() && (!/\d/.test(item[2]) || parseInt(item[2], 10) === 1)) return true;
  return tableAt(lines, i) !== null;
}

function parseBlocks(lines: string[]): Block[] {
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (isBlank(line)) {
      i++;
      continue;
    }
    const fence = fenceAt(line);
    if (fence) {
      // Without a closing fence (still streaming) the rest of the text is code.
      const code: string[] = [];
      for (i++; i < lines.length && !closesFence(lines[i], fence); i++) code.push(dedent(lines[i], fence.indent));
      i++;
      out.push({ kind: "code", lang: fence.lang, code: code.join("\n") });
      continue;
    }
    const heading = HEADING.exec(line);
    if (heading?.[2]) {
      out.push({ kind: "heading", level: heading[1].length, text: heading[2] });
      i++;
      continue;
    }
    if (RULE.test(line)) {
      out.push({ kind: "rule" });
      i++;
      continue;
    }
    if (QUOTE.test(line)) {
      const inner: string[] = [];
      for (; i < lines.length && QUOTE.test(lines[i]); i++) inner.push(lines[i].replace(QUOTE, ""));
      out.push({ kind: "quote", blocks: parseBlocks(inner) });
      continue;
    }
    if (ITEM.test(line)) {
      const list = parseList(lines, i);
      out.push(list.block);
      i = list.next;
      continue;
    }
    const table = tableAt(lines, i);
    if (table) {
      out.push(table.block);
      i = table.next;
      continue;
    }
    const para = [line.trim()];
    for (i++; i < lines.length && !isBlank(lines[i]) && !startsBlock(lines, i); i++) para.push(lines[i].trim());
    out.push({ kind: "paragraph", text: para.join("\n") });
  }
  return out;
}

/**
 * A list starting at line `start`. Lenient about nesting (as models write it):
 * any line indented deeper than the item marker belongs to the item.
 */
function parseList(lines: string[], start: number): { block: Block; next: number } {
  const first = ITEM.exec(lines[start])!;
  const base = columns(first[1]);
  const ordered = /\d/.test(first[2]);
  const sibling = (k: number) => {
    const m = ITEM.exec(lines[k]);
    return !!m && columns(m[1]) <= base + 1 && /\d/.test(m[2]) === ordered && !RULE.test(lines[k]);
  };
  const items: ListItem[] = [];
  let i = start;
  while (i < lines.length && sibling(i)) {
    const m = ITEM.exec(lines[i])!;
    const gap = !m[3] || columns(m[3]) > 4 ? 1 : columns(m[3]);
    const offset = columns(m[1]) + m[2].length + gap;
    let text = m[4] ?? "";
    let task: boolean | null = null;
    const tm = TASK.exec(text);
    if (tm) {
      task = tm[1] !== " ";
      text = text.slice(tm[0].length);
    }
    const head = [text];
    const body: string[] = [];
    let end = false;
    for (i++; i < lines.length; ) {
      const line = lines[i];
      if (isBlank(line)) {
        let j = i + 1;
        while (j < lines.length && isBlank(lines[j])) j++;
        if (j < lines.length && indentOf(lines[j]) > base + 1) {
          for (; i < j; i++) body.push("");
          continue;
        }
        if (j < lines.length && sibling(j)) i = j;
        else end = true;
        break;
      }
      if (indentOf(line) > base + 1) {
        body.push(line);
        i++;
        const f = fenceAt(line);
        if (f) {
          // Keep a fenced block together even if its lines are less indented.
          for (; i < lines.length && !closesFence(lines[i], f); i++) body.push(lines[i]);
          if (i < lines.length) body.push(lines[i++]);
        }
        continue;
      }
      if (sibling(i)) break;
      if (body.length === 0 && !startsBlock(lines, i)) {
        head.push(line.trim()); // lazy continuation of the item's first paragraph
        i++;
        continue;
      }
      end = true;
      break;
    }
    let d = offset;
    for (const l of body) if (!isBlank(l)) d = Math.min(d, indentOf(l));
    items.push({ task, blocks: parseBlocks([...head, ...body.map((l) => dedent(l, d))]) });
    if (end) break;
  }
  return { block: { kind: "list", ordered, start: ordered ? parseInt(first[2], 10) : 1, items }, next: i };
}

// ---------- Inline ----------

const ESCAPABLE = /[!-/:-@[-`{-~]/;
const WORD = /[\p{L}\p{N}]/u;
const SPACE = /\s/;
const BARE_URL = /https?:\/\/[^\s<>"'`]+/y;
const AUTOLINK = /<((?:https?:\/\/|mailto:)[^\s<>]+)>/iy;
const ENTITY = /&(?:#(\d{1,7})|#[xX]([\da-fA-F]{1,6})|(lt|gt|amp|quot|apos|nbsp));/g;
const NAMED_ENTITIES: Record<string, string> = { lt: "<", gt: ">", amp: "&", quot: '"', apos: "'", nbsp: " " };
const LINK = "font-medium text-accent underline decoration-accent/40 underline-offset-2 transition-colors hover:decoration-accent";
const CODE = "rounded-[4px] border border-border bg-surface-2 px-1 py-px font-mono text-[0.9em]";

/**
 * One inline text being parsed, with the positions after which a delimiter
 * (e.g. "*1", "`2") is known to have no closer — so many unmatched `*`, `_`,
 * `~~` or backticks don't make parsing quadratic while a reply streams.
 */
interface Scan {
  s: string;
  noCloser: Map<string, number>;
}

function runLength(s: string, i: number, ch: string): number {
  let j = i;
  while (s[j] === ch) j++;
  return j - i;
}

/** Decodes the few HTML entities models tend to write (plain text only, never code). */
function decodeEntities(s: string): string {
  if (!s.includes("&")) return s;
  return s.replace(ENTITY, (m, dec?: string, hex?: string, name?: string) => {
    if (name) return NAMED_ENTITIES[name];
    const cp = dec ? parseInt(dec, 10) : parseInt(hex ?? "", 16);
    return cp > 0 && cp <= 0x10ffff && (cp < 0xd800 || cp > 0xdfff) ? String.fromCodePoint(cp) : m;
  });
}

/** Backtick code span opening at i. */
function codeSpan(sc: Scan, i: number): { code: string; end: number } | null {
  const { s } = sc;
  const n = runLength(s, i, "`");
  const key = `\`${n}`;
  const miss = sc.noCloser.get(key);
  if (miss !== undefined && i >= miss) return null;
  for (let j = s.indexOf("`", i + n); j >= 0; ) {
    const m = runLength(s, j, "`");
    if (m === n) {
      let code = s.slice(i + n, j).replace(/\n/g, " ");
      if (code.startsWith(" ") && code.endsWith(" ") && code.trim()) code = code.slice(1, -1);
      return { code, end: j + m };
    }
    j = s.indexOf("`", j + m);
  }
  sc.noCloser.set(key, i);
  return null;
}

/** [text](url "title") starting at the "[" at i. */
function linkAt(sc: Scan, i: number): { text: string; url: string; end: number } | null {
  const { s } = sc;
  let depth = 0;
  let j = i;
  for (; j < s.length; j++) {
    const c = s[j];
    if (c === "\\") j++;
    else if (c === "`") j = (codeSpan(sc, j)?.end ?? j + runLength(s, j, "`")) - 1;
    else if (c === "[") depth++;
    else if (c === "]" && --depth === 0) break;
  }
  if (j >= s.length || s[j + 1] !== "(") return null;
  let k = j + 2;
  while (s[k] === " " || s[k] === "\t") k++;
  let url: string;
  if (s[k] === "<") {
    const e = s.indexOf(">", k);
    if (e < 0) return null;
    url = s.slice(k + 1, e);
    k = e + 1;
  } else {
    const from = k;
    let parens = 0;
    for (; k < s.length; k++) {
      const c = s[k];
      if (c === "\\") k++;
      else if (SPACE.test(c)) break;
      else if (c === "(") parens++;
      else if (c === ")") {
        if (parens === 0) break;
        parens--;
      }
    }
    url = s.slice(from, k).replace(/\\([!-/:-@[-`{-~])/g, "$1");
  }
  while (s[k] === " " || s[k] === "\t" || s[k] === "\n") k++;
  if (s[k] === '"' || s[k] === "'" || s[k] === "(") {
    const e = s.indexOf(s[k] === "(" ? ")" : s[k], k + 1);
    if (e < 0) return null;
    k = e + 1;
    while (s[k] === " " || s[k] === "\t") k++;
  }
  if (s[k] !== ")") return null;
  return { text: s.slice(i + 1, j), url, end: k + 1 };
}

/** Only web and mail links are rendered as links. */
function safeHref(url: string): string | null {
  const u = url.trim();
  return /^(?:https?:\/\/|mailto:)/i.test(u) ? u : null;
}

/** Drops trailing punctuation that belongs to the sentence, not the URL. */
function trimUrl(url: string): string {
  let u = url;
  const count = (ch: string) => u.split(ch).length - 1;
  for (;;) {
    const last = u[u.length - 1];
    if (/[.,:;!?'"*_~]/.test(last)) u = u.slice(0, -1);
    else if (last === ")" && count(")") > count("(")) u = u.slice(0, -1);
    else if (last === "]" && count("]") > count("[")) u = u.slice(0, -1);
    else return u;
  }
}

/** *em*, **strong**, ***both***, _em_, __strong__ and ~~strike~~ starting at i. */
function emphasis(sc: Scan, i: number, links: boolean): { node: ReactNode; end: number } | null {
  const { s } = sc;
  const ch = s[i];
  const n = runLength(s, i, ch);
  if (ch === "~" ? n !== 2 : n > 3) return null;
  const after = s[i + n];
  if (after === undefined || SPACE.test(after)) return null;
  if (ch === "_" && i > 0 && WORD.test(s[i - 1])) return null; // snake_case
  // Whether a run closes doesn't depend on where it opened: no closer after an
  // earlier opener means none after a later one either.
  const key = ch + n;
  const miss = sc.noCloser.get(key);
  if (miss !== undefined && i >= miss) return null;
  for (let j = i + n; j < s.length; ) {
    const c = s[j];
    if (c === "\\") {
      j += 2;
      continue;
    }
    if (c === "`") {
      j = codeSpan(sc, j)?.end ?? j + runLength(s, j, "`");
      continue;
    }
    if (c !== ch) {
      j++;
      continue;
    }
    const m = runLength(s, j, ch);
    const next = s[j + m];
    const opens = next !== undefined && !SPACE.test(next) && !ESCAPABLE.test(next);
    const closes = !SPACE.test(s[j - 1]) && (m === n || (m > n && !opens)) && !(ch === "_" && next !== undefined && WORD.test(next)) && (ch !== "~" || m === 2);
    if (closes) {
      // A longer closing run (***) closes with its last characters; the rest belongs to the inner text.
      const inner = s.slice(i + n, j + m - n);
      if (inner.trim()) {
        const kids = inline(inner, links);
        const node =
          ch === "~" ? (
            <del className="text-subtle">{kids}</del>
          ) : n === 1 ? (
            <em>{kids}</em>
          ) : n === 2 ? (
            <strong className="font-semibold">{kids}</strong>
          ) : (
            <strong className="font-semibold">
              <em>{kids}</em>
            </strong>
          );
        return { node, end: j + m };
      }
    }
    j += m;
  }
  sc.noCloser.set(key, i);
  return null;
}

function inline(s: string, links = true): ReactNode[] {
  const sc: Scan = { s, noCloser: new Map() };
  const out: ReactNode[] = [];
  let text = "";
  let key = 0;
  const push = (node: ReactNode) => {
    if (text) out.push(decodeEntities(text));
    text = "";
    out.push(<Fragment key={key++}>{node}</Fragment>);
  };
  const anchor = (href: string, children: ReactNode) => (
    <a href={href} target="_blank" rel="noreferrer" className={LINK}>
      {children}
    </a>
  );
  let i = 0;
  while (i < s.length) {
    const c = s[i];
    if (c === "\\" && i + 1 < s.length) {
      const next = s[i + 1];
      if (next === "\n") {
        push(<br />);
        i += 2;
        continue;
      }
      if (ESCAPABLE.test(next)) {
        text += next;
        i += 2;
        continue;
      }
    }
    if (c === "\n") {
      push(<br />);
      i++;
      continue;
    }
    if (c === "`") {
      const span = codeSpan(sc, i);
      if (span) {
        push(<code className={CODE}>{span.code}</code>);
        i = span.end;
        continue;
      }
      const n = runLength(s, i, "`");
      text += s.slice(i, i + n);
      i += n;
      continue;
    }
    if (links && (c === "[" || (c === "!" && s[i + 1] === "["))) {
      const image = c === "!";
      const link = linkAt(sc, image ? i + 1 : i);
      if (link) {
        const href = safeHref(link.url);
        // Images are never loaded: they become a link to the image.
        const kids = image ? decodeEntities(link.text) || link.url : inline(link.text, false);
        push(href ? anchor(href, kids) : kids);
        i = link.end;
        continue;
      }
    }
    if (c === "*" || c === "_" || c === "~") {
      const em = emphasis(sc, i, links);
      if (em) {
        push(em.node);
        i = em.end;
        continue;
      }
      const n = runLength(s, i, c);
      text += s.slice(i, i + n);
      i += n;
      continue;
    }
    if (links && c === "<") {
      AUTOLINK.lastIndex = i;
      const m = AUTOLINK.exec(s);
      if (m) {
        push(anchor(m[1], m[1]));
        i += m[0].length;
        continue;
      }
    }
    if (links && c === "h" && s.startsWith("http", i) && (i === 0 || !WORD.test(s[i - 1]))) {
      BARE_URL.lastIndex = i;
      const m = BARE_URL.exec(s);
      if (m) {
        const url = trimUrl(m[0]);
        if (/^https?:\/\/[^/\s]/.test(url)) {
          push(anchor(url, url));
          i += url.length;
          continue;
        }
      }
    }
    text += c;
    i++;
  }
  if (text) out.push(decodeEntities(text));
  return out;
}

// ---------- Rendering ----------

function CodeBlock({ lang, code }: { lang: string; code: string }) {
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-surface-2/50">
      <div className="flex h-7 items-center justify-between gap-2 border-b border-border pl-3 pr-0.5">
        <span className="truncate font-mono text-2xs text-subtle">{lang}</span>
        <CopyIconButton value={code} label={t("Copy code")} />
      </div>
      <pre className="overflow-x-auto px-3 py-2.5 font-mono text-[12px] leading-[1.65] text-fg">
        <code>{code}</code>
      </pre>
    </div>
  );
}

function TaskBox({ done }: { done: boolean }) {
  return (
    <span className={cn("mt-[3px] flex size-3.5 shrink-0 items-center justify-center rounded-[3px] border", done ? "border-inverse bg-inverse text-inverse-fg" : "border-border-strong bg-surface")}>
      {done && <Check className="size-2.5" strokeWidth={3} />}
    </span>
  );
}

const alignStyle = (a: Align) => (a ? { textAlign: a } : undefined);

function renderBlocks(blocks: Block[]): ReactNode[] {
  return blocks.map((b, i) => {
    switch (b.kind) {
      case "paragraph":
        return <p key={i}>{inline(b.text)}</p>;
      case "heading": {
        const cls = cn("font-semibold leading-snug tracking-[-0.01em] [&:not(:first-child)]:pt-1.5", b.level === 1 ? "text-[15px]" : b.level === 2 ? "text-[14px]" : "text-[13px]");
        return b.level <= 2 ? (
          <h3 key={i} className={cls}>
            {inline(b.text)}
          </h3>
        ) : (
          <h4 key={i} className={cls}>
            {inline(b.text)}
          </h4>
        );
      }
      case "code":
        return <CodeBlock key={i} lang={b.lang} code={b.code} />;
      case "rule":
        return <hr key={i} className="border-border" />;
      case "quote":
        return (
          <blockquote key={i} className="space-y-2 border-l-2 border-border-strong pl-3 text-muted">
            {renderBlocks(b.blocks)}
          </blockquote>
        );
      case "list": {
        const items = b.items.map((it, j) => (
          <li key={j} className={cn("pl-0.5", it.task !== null && "-ml-5 flex list-none gap-2 pl-0")}>
            {it.task !== null && <TaskBox done={it.task} />}
            <div className="min-w-0 flex-1 space-y-1.5">{renderBlocks(it.blocks)}</div>
          </li>
        ));
        return b.ordered ? (
          <ol key={i} start={b.start !== 1 ? b.start : undefined} className="list-decimal space-y-1 pl-5 marker:text-subtle">
            {items}
          </ol>
        ) : (
          <ul key={i} className="list-disc space-y-1 pl-5 marker:text-faint [&_ul]:list-[circle]">
            {items}
          </ul>
        );
      }
      case "table":
        return (
          <div key={i} className="overflow-x-auto rounded-lg border border-border [overflow-wrap:normal]">
            <table className="min-w-full border-collapse text-[12.5px] leading-snug">
              <thead className="bg-surface-2/70">
                <tr>
                  {b.head.map((cell, k) => (
                    <th key={k} className="px-2.5 py-1.5 text-left font-semibold" style={alignStyle(b.align[k])}>
                      {inline(cell)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {b.rows.map((row, k) => (
                  <tr key={k} className="border-t border-border">
                    {row.map((cell, l) => (
                      <td key={l} className="px-2.5 py-1.5 align-top" style={alignStyle(b.align[l])}>
                        {inline(cell)}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        );
    }
  });
}

/** Renders Markdown text (complete or still streaming) as React elements. */
export const Markdown = memo(function Markdown({ text, className }: { text: string; className?: string }) {
  const blocks = parseBlocks(text.replace(/\r\n?/g, "\n").split("\n"));
  return <div className={cn("min-w-0 space-y-2.5 text-[13px] leading-relaxed text-fg [overflow-wrap:anywhere]", className)}>{renderBlocks(blocks)}</div>;
});
