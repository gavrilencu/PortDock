import { useMemo, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Download, History, Lock, Play, RefreshCw, Search, ShieldAlert, Table2, TerminalSquare, Unlock } from "lucide-react";
import { api } from "../lib/api";
import type { DatabaseDetail } from "../lib/types";
import { cn } from "../lib/utils";
import { t, tMaybe, tx } from "../lib/i18n";
import { Badge, Button, Card, Empty, ErrorBox, Input, Loading, Switch } from "./ui";

interface Result {
  statement: string;
  columns: string[] | null;
  rows: (string | null)[][] | null;
  row_count: number;
  affected: number;
  truncated: boolean;
}

interface QueryOutput {
  results: Result[];
  error: string;
  read_only: boolean;
  duration_ms: number;
  output_truncated: boolean;
}

interface ConsoleInfo {
  supported: boolean;
  can_write: boolean;
  max_rows: number;
  timeout: number;
}

const historyKey = (id: number) => `portdock.sql-history.${id}`;

function loadHistory(id: number): string[] {
  try {
    return JSON.parse(localStorage.getItem(historyKey(id)) ?? "[]");
  } catch {
    return [];
  }
}

function saveHistory(id: number, list: string[]) {
  try {
    localStorage.setItem(historyKey(id), JSON.stringify(list.slice(0, 20)));
  } catch {
    /* private mode */
  }
}

/** SELECT of the first rows of a table, quoted for the engine. */
function previewQuery(engine: string, schema: string | null, name: string) {
  switch (engine) {
    case "postgres":
      return `SELECT * FROM "${(schema ?? "public").replace(/"/g, '""')}"."${name.replace(/"/g, '""')}" LIMIT 100;`;
    case "mssql":
      return `SELECT TOP 100 * FROM [${(schema ?? "dbo").replace(/]/g, "]]")}].[${name.replace(/]/g, "]]")}];`;
    default:
      return `SELECT * FROM \`${name.replace(/`/g, "``")}\` LIMIT 100;`;
  }
}

const examples: Record<string, string> = {
  postgres: "SELECT now(), version();",
  mysql: "SELECT NOW(), VERSION();",
  mariadb: "SELECT NOW(), VERSION();",
  mssql: "SELECT GETDATE() AS now, @@VERSION AS version;",
  clickhouse: "SELECT now(), version();",
};

function csv(r: Result) {
  const esc = (v: string | null) => (v === null ? "" : /[",\n\r]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v);
  return [r.columns ?? [], ...(r.rows ?? [])].map((row) => row.map(esc).join(",")).join("\r\n");
}

function download(name: string, text: string) {
  const url = URL.createObjectURL(new Blob(["\ufeff" + text], { type: "text/csv;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function SQLConsole({ db }: { db: DatabaseDetail }) {
  const info = useQuery({ queryKey: ["db-console", db.id], queryFn: () => api.get<ConsoleInfo>(`/databases/${db.id}/console`) });
  const running = db.status === "running";
  const tables = useQuery({
    queryKey: ["db-tables", db.id],
    queryFn: () => api.get<Result>(`/databases/${db.id}/tables`),
    enabled: !!info.data?.supported && running,
    retry: false,
  });
  const [sql, setSql] = useState(() => loadHistory(db.id)[0] ?? examples[db.engine] ?? "SELECT 1;");
  const [write, setWrite] = useState(false);
  const [history, setHistory] = useState(() => loadHistory(db.id));
  const [showHistory, setShowHistory] = useState(false);
  const [filter, setFilter] = useState("");
  const editor = useRef<HTMLTextAreaElement>(null);

  const run = useMutation({
    mutationFn: (query: string) => api.post<QueryOutput>(`/databases/${db.id}/query`, { query, write }),
    onSuccess: (_, query) => {
      const next = [query, ...history.filter((h) => h !== query)].slice(0, 20);
      setHistory(next);
      saveHistory(db.id, next);
      if (write && /\b(create|drop|alter|rename)\b/i.test(query)) tables.refetch();
    },
  });

  const exec = (text?: string) => {
    const el = editor.current;
    const selected = el && el.selectionEnd > el.selectionStart ? sql.slice(el.selectionStart, el.selectionEnd) : "";
    const q = (text ?? (selected || sql)).trim();
    if (q) run.mutate(q);
  };

  const tableRows = useMemo(() => {
    const r = tables.data;
    if (!r?.columns) return [];
    const col = (n: string) => r.columns!.indexOf(n);
    const [si, ni, ti, ei] = [col("schema"), col("name"), col("type"), col("row_estimate")];
    const needle = filter.trim().toLowerCase();
    return (r.rows ?? [])
      .map((row) => ({ schema: si >= 0 ? row[si] : null, name: row[ni] ?? "", type: ti >= 0 ? (row[ti] ?? "") : "", rows: ei >= 0 ? row[ei] : null }))
      .filter((x) => !needle || `${x.schema ?? ""}.${x.name}`.toLowerCase().includes(needle));
  }, [tables.data, filter]);

  if (info.isLoading) return <Loading />;
  if (!info.data?.supported)
    return <Empty icon={<TerminalSquare />} title={t("The SQL console is not available for this engine")} description={t("Use the Terminal tab to open the database's own client.")} />;
  if (!running) return <Empty icon={<TerminalSquare />} title={t("Database is not running")} description={t("Start it to run queries.")} />;

  const out = run.data;
  return (
    <div className="grid gap-6 xl:grid-cols-[260px_minmax(0,1fr)]">
      <Card
        title={t("Tables")}
        actions={
          <Button size="icon-sm" variant="ghost" title={t("Refresh")} onClick={() => tables.refetch()} loading={tables.isFetching}>
            <RefreshCw />
          </Button>
        }
        className="self-start"
      >
        <div className="border-b border-border p-2">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
            <Input className="pl-8" placeholder={t("Filter…")} value={filter} onChange={(e) => setFilter(e.target.value)} />
          </div>
        </div>
        <div className="max-h-[560px] overflow-y-auto py-1">
          {tables.isLoading ? (
            <div className="p-3">
              <Loading rows={4} />
            </div>
          ) : tables.error ? (
            <div className="p-3">
              <ErrorBox error={tables.error} />
            </div>
          ) : tableRows.length === 0 ? (
            <p className="px-4 py-6 text-center text-xs text-subtle">{filter ? t("No tables match.") : t("No tables yet.")}</p>
          ) : (
            tableRows.map((tb) => (
              <button
                key={`${tb.schema}.${tb.name}`}
                className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[13px] hover:bg-surface-2"
                title={t("Show the first 100 rows")}
                onClick={() => {
                  const q = previewQuery(db.engine, tb.schema, tb.name);
                  setSql(q);
                  run.mutate(q);
                }}
              >
                <Table2 className={cn("size-3.5 shrink-0", /view/i.test(tb.type) ? "text-info" : "text-faint")} />
                <span className="min-w-0 flex-1 truncate font-mono text-xs">
                  {tb.schema && tb.schema !== "public" && tb.schema !== "dbo" && <span className="text-subtle">{tb.schema}.</span>}
                  {tb.name}
                </span>
                {tb.rows && <span className="shrink-0 text-2xs tabular-nums text-faint">{Number(tb.rows).toLocaleString()}</span>}
              </button>
            ))
          )}
        </div>
      </Card>

      <div className="min-w-0 space-y-4">
        <Card>
          <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-2.5">
            {write ? (
              <Badge tone="danger">
                <Unlock className="size-3" /> {t("Writes allowed")}
              </Badge>
            ) : (
              <Badge tone="success">
                <Lock className="size-3" /> {t("Read-only")}
              </Badge>
            )}
            <span className="text-xs text-subtle">{t("Ctrl + Enter runs the script, or only the selected text.")}</span>
            <div className="ml-auto flex items-center gap-2">
              {info.data.can_write && <Switch checked={write} onChange={setWrite} label={t("Allow writes")} />}
              <Button size="sm" variant="ghost" icon={<History className="size-3.5" />} onClick={() => setShowHistory(!showHistory)} disabled={!history.length}>
                {t("History")}
              </Button>
              <Button size="sm" variant="primary" icon={<Play className="size-3.5" />} loading={run.isPending} onClick={() => exec()}>
                {t("Run")}
              </Button>
            </div>
          </div>
          {showHistory && history.length > 0 && (
            <div className="max-h-48 overflow-y-auto border-b border-border bg-surface-2/40">
              {history.map((h, i) => (
                <button
                  key={i}
                  className="block w-full truncate px-4 py-1.5 text-left font-mono text-xs text-muted hover:bg-surface-2 hover:text-fg"
                  onClick={() => {
                    setSql(h);
                    setShowHistory(false);
                  }}
                >
                  {h.replace(/\s+/g, " ")}
                </button>
              ))}
            </div>
          )}
          <textarea
            ref={editor}
            value={sql}
            onChange={(e) => setSql(e.target.value)}
            spellCheck={false}
            rows={8}
            className="block min-h-40 w-full resize-y bg-transparent px-4 py-3 font-mono text-[13px] leading-relaxed text-fg outline-none"
            onKeyDown={(e) => {
              if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
                e.preventDefault();
                exec();
              } else if (e.key === "Tab" && !e.shiftKey) {
                e.preventDefault();
                const el = e.currentTarget;
                const s = el.selectionStart;
                setSql(sql.slice(0, s) + "  " + sql.slice(el.selectionEnd));
                requestAnimationFrame(() => el.setSelectionRange(s + 2, s + 2));
              }
            }}
          />
          {write && (
            <div className="flex items-center gap-2 border-t border-danger/25 bg-danger-soft px-4 py-2 text-xs text-danger">
              <ShieldAlert className="size-3.5 shrink-0" />
              {t("Statements change the data directly, without a transaction to undo them. Every query is recorded in the audit log.")}
            </div>
          )}
        </Card>

        {!info.data.can_write && (
          <p className="text-xs text-subtle">
            {tx("You have read-only access: {kinds} statements run in a session that cannot change data. Organization admins can allow writes.", {
              kinds: <span className="font-medium text-fg">SELECT, SHOW, EXPLAIN</span>,
            })}
          </p>
        )}

        {run.isPending && <Loading rows={3} />}
        {run.error && !run.isPending && (
          <div className="rounded-lg border border-danger/25 bg-danger-soft px-4 py-3">
            <div className="text-xs font-medium text-danger">{t("The query was not run")}</div>
            <p className="mt-1 text-xs text-danger">{tMaybe(run.error.message)}</p>
          </div>
        )}
        {out && !run.isPending && (
          <div className="space-y-4">
            <div className="text-xs text-subtle">
              {t("{n} statement ran in {ms} ms|{n} statements ran in {ms} ms", { n: out.results.length, ms: out.duration_ms })}
              {out.read_only && ` · ${t("read-only")}`}
            </div>
            {out.results.map((r, i) => (
              <ResultCard key={i} r={r} index={i} dbName={db.name} maxRows={info.data!.max_rows} />
            ))}
            {out.error && (
              <div className="rounded-lg border border-danger/25 bg-danger-soft px-4 py-3">
                <div className="text-xs font-medium text-danger">{out.results.length ? t("Statement {n} failed; the next ones did not run", { n: out.results.length + 1 }) : t("The query failed")}</div>
                <pre className="mt-1 whitespace-pre-wrap break-words font-mono text-xs text-danger">{tMaybe(out.error)}</pre>
              </div>
            )}
            {out.output_truncated && <p className="text-xs text-warning">{t("The output was too large; later results are missing. Add a LIMIT.")}</p>}
          </div>
        )}
      </div>
    </div>
  );
}

function ResultCard({ r, index, dbName, maxRows }: { r: Result; index: number; dbName: string; maxRows: number }) {
  const [all, setAll] = useState(false);
  const rows = r.rows ?? [];
  const shown = all ? rows : rows.slice(0, 200);
  const summary =
    r.columns === null
      ? r.affected > 0
        ? t("{n} row affected|{n} rows affected", { n: r.affected })
        : t("Done")
      : t("{n} row|{n} rows", { n: r.row_count });
  return (
    <Card
      title={<span className="block truncate font-mono text-xs font-normal text-muted" title={r.statement}>{r.statement.replace(/\s+/g, " ")}</span>}
      description={
        <span>
          {summary}
          {r.truncated && ` · ${t("first {n} shown", { n: maxRows })}`}
        </span>
      }
      actions={
        r.columns !== null &&
        r.columns.length > 0 && (
          <Button size="sm" variant="ghost" icon={<Download className="size-3.5" />} onClick={() => download(`${dbName}-result-${index + 1}.csv`, csv(r))}>
            CSV
          </Button>
        )
      }
    >
      {r.columns !== null && r.columns.length > 0 && (
        <>
          <div className="max-h-[520px] overflow-auto">
            <table className="w-full border-collapse text-left font-mono text-xs">
              <thead className="sticky top-0 z-10 bg-surface-2">
                <tr>
                  {r.columns.map((c, i) => (
                    <th key={i} className="whitespace-nowrap border-b border-border px-3 py-2 font-semibold text-fg">
                      {c || <span className="text-faint">?column?</span>}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {shown.map((row, i) => (
                  <tr key={i} className="border-b border-border/60 last:border-0 hover:bg-surface-2/50">
                    {r.columns!.map((_, k) => {
                      const v = row[k];
                      return (
                        <td key={k} className="max-w-md whitespace-pre-wrap break-words px-3 py-1.5 align-top">
                          {v === null || v === undefined ? <span className="italic text-faint">NULL</span> : v}
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {rows.length > shown.length && (
            <div className="border-t border-border px-4 py-2">
              <Button size="sm" variant="ghost" onClick={() => setAll(true)}>
                {t("Show all {n} rows", { n: rows.length })}
              </Button>
            </div>
          )}
        </>
      )}
    </Card>
  );
}
