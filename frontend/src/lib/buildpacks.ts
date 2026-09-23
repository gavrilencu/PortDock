import type { BuildPack } from "./types";
import { tk } from "./i18n";

export type BuildField = "dockerfile_path" | "compose" | "install" | "build" | "start" | "publish" | "base_image" | "port";

export interface BuildPackInfo {
  id: BuildPack;
  label: string;
  description: string;
  group: "Container" | "Language" | "Other";
  fields: BuildField[];
  placeholders?: Partial<Record<"install" | "build" | "start", string>>;
  /** Start command is mandatory for this pack. */
  startRequired?: boolean;
  defaultPort?: number;
}

export const BUILD_PACKS: BuildPackInfo[] = [
  { id: "auto", label: tk("Auto-detect"), group: "Other", description: tk("Uses a Dockerfile when present, otherwise detects Node, Bun, Python, Go or static sites."), fields: ["install", "build", "start", "port"] },
  { id: "dockerfile", label: "Dockerfile", group: "Container", description: tk("Build the image with the Dockerfile in your repository."), fields: ["dockerfile_path", "start", "port"] },
  { id: "compose", label: "Docker Compose", group: "Container", description: tk("Run a multi-container stack from a compose file."), fields: ["compose"] },
  {
    id: "node",
    label: "Node.js",
    group: "Language",
    description: tk("npm, pnpm or yarn — detected from your lockfile."),
    fields: ["install", "build", "start", "port"],
    placeholders: { install: "npm ci", build: "npm run build", start: "npm run start" },
    defaultPort: 3000,
  },
  { id: "bun", label: "Bun", group: "Language", description: tk("Install, build and run with the Bun runtime."), fields: ["install", "build", "start", "port"], placeholders: { install: "bun install", build: "bun run build", start: "bun run start" }, defaultPort: 3000 },
  {
    id: "python",
    label: "Python",
    group: "Language",
    description: tk("pip install from requirements.txt or pyproject.toml."),
    fields: ["install", "build", "start", "port"],
    placeholders: { install: "pip install -r requirements.txt", start: "gunicorn app:app --bind 0.0.0.0:8000" },
    defaultPort: 8000,
  },
  { id: "go", label: "Go", group: "Language", description: tk("Compile a static binary and run it on Alpine."), fields: ["build", "start", "port"], placeholders: { build: "CGO_ENABLED=0 go build -o /out/app .", start: "/app" }, defaultPort: 8080 },
  { id: "static", label: tk("Static site"), group: "Language", description: tk("Optionally build with Node, then serve with nginx (SPA fallback included)."), fields: ["install", "build", "publish"], placeholders: { install: "npm ci", build: "npm run build" }, defaultPort: 80 },
  {
    id: "shell",
    label: tk("Shell script"),
    group: "Other",
    description: tk("Run your own install, build and start commands on any base image."),
    fields: ["base_image", "install", "build", "start", "port"],
    placeholders: { install: "apt-get update && apt-get install -y make", build: "make build", start: "./bin/server" },
    startRequired: true,
  },
  { id: "nixpacks", label: "Nixpacks", group: "Other", description: tk("Zero-config builds by Railway's Nixpacks (installed on the server)."), fields: ["install", "build", "start", "port"] },
];

export const buildPack = (id: string) => BUILD_PACKS.find((b) => b.id === id) ?? BUILD_PACKS[0];
