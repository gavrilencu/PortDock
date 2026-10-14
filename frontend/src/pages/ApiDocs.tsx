import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { BookOpen, Download, Search } from "lucide-react";
import { api } from "../lib/api";
import { t } from "../lib/i18n";
import { Badge, Button, Card, ErrorBox, Input, Loading, PageHeader, Table } from "../components/ui";

type Operation = { summary: string; tags: string[]; "x-token-scope"?: string; "x-access"?: string; security?: unknown[] };
type Spec = { info: { version: string }; paths: Record<string, Record<string, Operation>> };

const methodTone: Record<string, "success" | "info" | "warning" | "danger" | "neutral"> = { get: "success", post: "info", put: "warning", patch: "warning", delete: "danger" };
const scopeTone: Record<string, "neutral" | "info" | "warning" | "danger"> = { read: "neutral", deploy: "info", write: "warning", full: "danger" };

/** The PortDock API, from the generated OpenAPI document. */
export function ApiDocsPage() {
  const spec = useQuery({ queryKey: ["openapi"], queryFn: () => api.get<Spec>("/openapi.json"), staleTime: Infinity });
  const [q, setQ] = useState("");
  const groups = useMemo(() => {
    const out = new Map<string, { method: string; path: string; op: Operation }[]>();
    for (const [path, ops] of Object.entries(spec.data?.paths ?? {})) {
      for (const [method, op] of Object.entries(ops)) {
        const text = `${method} ${path} ${op.summary}`.toLowerCase();
        if (q && !text.includes(q.toLowerCase())) continue;
        const tag = op.tags?.[0] ?? "other";
        if (!out.has(tag)) out.set(tag, []);
        out.get(tag)!.push({ method, path, op });
      }
    }
    return [...out.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [spec.data, q]);
  if (spec.isLoading) return <Loading />;
  if (spec.error) return <ErrorBox error={spec.error} />;
  const count = groups.reduce((n, [, ops]) => n + ops.length, 0);
  return (
    <>
      <PageHeader
        icon={<BookOpen className="size-5" />}
        title={t("API reference")}
        subtitle={t("{n} operations. Authenticate with Authorization: Bearer <token>; the scope column is the smallest token scope that may call each one.", { n: count })}
        actions={
          <a href="/api/openapi.json" download="portdock-openapi.json">
            <Button icon={<Download className="size-4" />}>{t("OpenAPI document")}</Button>
          </a>
        }
      />
      <div className="relative mb-4 max-w-sm">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
        <Input className="pl-8" placeholder={t("Search operations")} value={q} onChange={(e) => setQ(e.target.value)} />
      </div>
      <div className="space-y-6">
        {groups.map(([tag, ops]) => (
          <Card key={tag} title={<span className="capitalize">{tag}</span>}>
            <Table>
              <tbody>
                {ops
                  .sort((a, b) => a.path.localeCompare(b.path) || a.method.localeCompare(b.method))
                  .map(({ method, path, op }) => (
                    <tr key={method + path}>
                      <td className="w-20">
                        <Badge tone={methodTone[method] ?? "neutral"}>{method.toUpperCase()}</Badge>
                      </td>
                      <td className="font-mono text-[13px]">{path}</td>
                      <td className="text-muted">{op.summary}</td>
                      <td className="w-28 text-right">
                        {op.security && op.security.length === 0 ? (
                          <Badge>{t("public")}</Badge>
                        ) : (
                          <Badge tone={scopeTone[op["x-token-scope"] ?? ""] ?? "neutral"}>{op["x-token-scope"]}</Badge>
                        )}
                      </td>
                    </tr>
                  ))}
              </tbody>
            </Table>
          </Card>
        ))}
      </div>
    </>
  );
}
