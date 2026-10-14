// Translations for components/mcp.tsx (Settings → API tokens → AI agents).
export const ro: Record<string, string> = {
  "AI agents (MCP)": "Agenți AI (MCP)",
  "PortDock is a Model Context Protocol server: an AI agent can work with your applications through tools, with an API token. The token's scope and projects apply to every tool: a read token can only look, a deploy token can also deploy and roll back.":
    "PortDock este un server Model Context Protocol: un agent AI poate lucra cu aplicațiile tale prin unelte, cu un token API. Scope-ul și proiectele token-ului se aplică fiecărei unelte: un token read poate doar să vadă, un token deploy poate și să facă deploy și rollback.",
  "MCP endpoint (Streamable HTTP)": "Endpoint MCP (Streamable HTTP)",
  "Other clients (stdio)": "Alți clienți (stdio)",
  "Run it in a terminal; the agent can then list applications, deploy, follow a deployment, read logs and roll back.":
    "Rulează comanda într-un terminal; apoi agentul poate lista aplicațiile, face deploy, urmări un deploy, citi logurile și face rollback.",
  "Save it as .vscode/mcp.json; VS Code asks for the token once and keeps it in its secret storage.":
    "Salvează-l ca .vscode/mcp.json; VS Code cere token-ul o singură dată și îl păstrează în stocarea lui de secrete.",
  "For clients that start a local command (Claude Desktop, Cursor, Windsurf…): pdctl mcp passes the messages to PortDock with the token. Without env, it uses the pdctl login.":
    "Pentru clienții care pornesc o comandă locală (Claude Desktop, Cursor, Windsurf…): pdctl mcp transmite mesajele către PortDock cu token-ul. Fără env, folosește autentificarea din pdctl login.",
  "Tools: list projects, applications, databases, servers and alerts; deploy (and wait), deployment status and log, application logs, roll back, restart, back up a database. Every action an agent takes is in the audit log.":
    "Unelte: listarea proiectelor, aplicațiilor, bazelor de date, serverelor și alertelor; deploy (cu așteptare), starea și logul unui deploy, logurile aplicației, rollback, restart, backup pentru o bază de date. Fiecare acțiune a unui agent apare în audit log.",
};

export const ru: Record<string, string> = {
  "AI agents (MCP)": "ИИ-агенты (MCP)",
  "PortDock is a Model Context Protocol server: an AI agent can work with your applications through tools, with an API token. The token's scope and projects apply to every tool: a read token can only look, a deploy token can also deploy and roll back.":
    "PortDock — сервер Model Context Protocol: ИИ-агент может работать с вашими приложениями через инструменты по API-токену. Область действия и проекты токена применяются к каждому инструменту: токен read может только смотреть, токен deploy может также деплоить и откатывать.",
  "MCP endpoint (Streamable HTTP)": "Эндпоинт MCP (Streamable HTTP)",
  "Other clients (stdio)": "Другие клиенты (stdio)",
  "Run it in a terminal; the agent can then list applications, deploy, follow a deployment, read logs and roll back.":
    "Выполните в терминале; после этого агент сможет просматривать приложения, деплоить, следить за деплоем, читать логи и откатывать.",
  "Save it as .vscode/mcp.json; VS Code asks for the token once and keeps it in its secret storage.":
    "Сохраните как .vscode/mcp.json; VS Code один раз запросит токен и сохранит его в хранилище секретов.",
  "For clients that start a local command (Claude Desktop, Cursor, Windsurf…): pdctl mcp passes the messages to PortDock with the token. Without env, it uses the pdctl login.":
    "Для клиентов, запускающих локальную команду (Claude Desktop, Cursor, Windsurf…): pdctl mcp передаёт сообщения в PortDock с токеном. Без env используется вход из pdctl login.",
  "Tools: list projects, applications, databases, servers and alerts; deploy (and wait), deployment status and log, application logs, roll back, restart, back up a database. Every action an agent takes is in the audit log.":
    "Инструменты: списки проектов, приложений, баз данных, серверов и оповещений; деплой (с ожиданием), статус и лог деплоя, логи приложения, откат, перезапуск, резервная копия базы данных. Каждое действие агента записывается в журнал аудита.",
};
