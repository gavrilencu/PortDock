import { useState } from "react";
import { Card, CopyButton, CopyField, SegmentedControl } from "./ui";
import { t } from "../lib/i18n";

// Settings → API tokens: how to connect an AI agent to the MCP endpoint.

type Client = "claude-code" | "vscode" | "stdio";

export function McpCard() {
  const url = `${window.location.origin}/api/mcp`;
  const [client, setClient] = useState<Client>("claude-code");
  const configs: Record<Client, { snippet: string; hint: string }> = {
    "claude-code": {
      snippet: `claude mcp add --transport http portdock ${url} \\
  --header "Authorization: Bearer pd_…"`,
      hint: t("Run it in a terminal; the agent can then list applications, deploy, follow a deployment, read logs and roll back."),
    },
    vscode: {
      snippet: `{
  "servers": {
    "portdock": {
      "type": "http",
      "url": "${url}",
      "headers": { "Authorization": "Bearer \${input:portdock-token}" }
    }
  },
  "inputs": [
    { "id": "portdock-token", "type": "promptString", "description": "PortDock API token", "password": true }
  ]
}`,
      hint: t("Save it as .vscode/mcp.json; VS Code asks for the token once and keeps it in its secret storage."),
    },
    stdio: {
      snippet: `{
  "mcpServers": {
    "portdock": {
      "command": "pdctl",
      "args": ["mcp"],
      "env": { "PORTDOCK_URL": "${window.location.origin}", "PORTDOCK_TOKEN": "pd_…" }
    }
  }
}`,
      hint: t("For clients that start a local command (Claude Desktop, Cursor, Windsurf…): pdctl mcp passes the messages to PortDock with the token. Without env, it uses the pdctl login."),
    },
  };
  const c = configs[client];
  return (
    <Card
      title={t("AI agents (MCP)")}
      description={t("PortDock is a Model Context Protocol server: an AI agent can work with your applications through tools, with an API token. The token's scope and projects apply to every tool: a read token can only look, a deploy token can also deploy and roll back.")}
    >
      <div className="space-y-4 p-5">
        <CopyField label={t("MCP endpoint (Streamable HTTP)")} value={url} />
        <SegmentedControl
          value={client}
          onChange={setClient}
          className="[&>button]:normal-case"
          options={[
            { value: "claude-code", label: "Claude Code" },
            { value: "vscode", label: "VS Code" },
            { value: "stdio", label: t("Other clients (stdio)") },
          ]}
        />
        <div className="relative">
          <pre className="overflow-x-auto rounded-lg border border-border bg-surface-2/60 p-3 pr-10 font-mono text-2xs leading-relaxed">{c.snippet}</pre>
          <CopyButton value={c.snippet} className="absolute right-2 top-2" />
        </div>
        <p className="text-xs text-muted">{c.hint}</p>
        <p className="text-xs text-muted">
          {t("Tools: list projects, applications, databases, servers and alerts; deploy (and wait), deployment status and log, application logs, roll back, restart, back up a database. Every action an agent takes is in the audit log.")}
        </p>
      </div>
    </Card>
  );
}
