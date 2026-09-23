import type { ReactNode } from "react";
import { Box, Boxes, Code2, Container, FileCode, Globe, Sparkles, SquareTerminal, Wand2 } from "lucide-react";
import { BUILD_PACKS, buildPack } from "../lib/buildpacks";
import type { BuildPack, BuildSuggestion } from "../lib/types";
import { cn } from "../lib/utils";
import { Badge, Field, Input } from "./ui";

export interface BuildValues {
  build_pack: BuildPack;
  dockerfile_path: string;
  compose_path: string;
  compose_service: string;
  install_command: string;
  build_command: string;
  start_command: string;
  publish_directory: string;
  docker_image: string;
  port: number;
}

const icons: Record<string, ReactNode> = {
  auto: <Wand2 />,
  dockerfile: <Container />,
  compose: <Boxes />,
  node: <Code2 />,
  bun: <Code2 />,
  python: <Code2 />,
  go: <Code2 />,
  static: <Globe />,
  shell: <SquareTerminal />,
  nixpacks: <Box />,
};

/** Deployment type picker plus the fields that type needs. */
export function BuildSettings<T extends BuildValues>({ value, onChange, suggestion, hidePort }: { value: T; onChange: (patch: Partial<BuildValues>) => void; suggestion?: BuildSuggestion | null; hidePort?: boolean }) {
  const info = buildPack(value.build_pack);
  const has = (f: string) => info.fields.includes(f as never) && !(hidePort && f === "port");
  const choose = (id: BuildPack) => {
    const next = buildPack(id);
    const patch: Partial<BuildValues> = { build_pack: id };
    if (next.defaultPort && (value.port === 0 || value.port === buildPack(value.build_pack).defaultPort || value.port === 3000)) patch.port = next.defaultPort;
    onChange(patch);
  };

  return (
    <div className="space-y-5">
      <div>
        <div className="mb-2 flex items-center justify-between">
          <span className="text-[13px] font-medium">Deployment type</span>
          {suggestion && (
            <span className="flex items-center gap-1.5 text-xs text-subtle">
              <Sparkles className="size-3.5" /> {suggestion.reason}
            </span>
          )}
        </div>
        <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-5">
          {BUILD_PACKS.map((b) => {
            const active = value.build_pack === b.id;
            return (
              <button
                type="button"
                key={b.id}
                onClick={() => choose(b.id)}
                title={b.description}
                className={cn(
                  "flex items-center gap-2.5 rounded-lg border px-3 py-2.5 text-left transition-colors [&_svg]:size-4",
                  active ? "border-fg bg-surface-2 ring-1 ring-fg" : "border-border bg-surface hover:border-border-strong",
                )}
              >
                <span className={cn(active ? "text-fg" : "text-faint")}>{icons[b.id] ?? <FileCode />}</span>
                <span className="min-w-0 flex-1 truncate text-[13px] font-medium">{b.label}</span>
                {suggestion?.build_pack === b.id && <Badge tone="accent">Suggested</Badge>}
              </button>
            );
          })}
        </div>
        <p className="mt-2 text-xs text-subtle">{info.description}</p>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        {has("dockerfile_path") && (
          <Field label="Dockerfile path" hint="Relative to the base directory.">
            <Input value={value.dockerfile_path} onChange={(e) => onChange({ dockerfile_path: e.target.value })} />
          </Field>
        )}
        {has("compose") && (
          <>
            <Field label="Compose file" hint="Relative to the base directory.">
              <Input value={value.compose_path} onChange={(e) => onChange({ compose_path: e.target.value })} />
            </Field>
            <Field label="Exposed service" hint="Receives the domain. Defaults to the first service.">
              <Input value={value.compose_service} onChange={(e) => onChange({ compose_service: e.target.value })} placeholder="web" />
            </Field>
          </>
        )}
        {has("base_image") && (
          <Field label="Base image" hint="Any Docker image; your commands run inside it.">
            <Input value={value.docker_image} onChange={(e) => onChange({ docker_image: e.target.value })} placeholder="debian:bookworm-slim" />
          </Field>
        )}
        {has("install") && (
          <Field label="Install command" hint={value.build_pack === "shell" ? "Runs once during the image build." : "Leave empty to use the default."}>
            <Input className="font-mono text-xs" value={value.install_command} onChange={(e) => onChange({ install_command: e.target.value })} placeholder={info.placeholders?.install ?? "auto"} />
          </Field>
        )}
        {has("build") && (
          <Field label="Build command" hint="Leave empty to use the default.">
            <Input className="font-mono text-xs" value={value.build_command} onChange={(e) => onChange({ build_command: e.target.value })} placeholder={info.placeholders?.build ?? "auto"} />
          </Field>
        )}
        {has("start") && (
          <Field label="Start command" required={info.startRequired} hint={info.startRequired ? "The long-running process of your application." : "Overrides the default start command."}>
            <Input className="font-mono text-xs" value={value.start_command} onChange={(e) => onChange({ start_command: e.target.value })} placeholder={info.placeholders?.start ?? "auto"} />
          </Field>
        )}
        {has("publish") && (
          <Field label="Output directory" hint="Folder with the built static files.">
            <Input value={value.publish_directory} onChange={(e) => onChange({ publish_directory: e.target.value })} placeholder="dist" />
          </Field>
        )}
        {has("port") && (
          <Field label="Port" hint="The port your application listens on inside the container.">
            <Input type="number" value={value.port} onChange={(e) => onChange({ port: Number(e.target.value) })} />
          </Field>
        )}
      </div>
    </div>
  );
}

/** Applies a detection result to build values. */
export function applySuggestion(s: BuildSuggestion): Partial<BuildValues> {
  return {
    build_pack: s.build_pack,
    port: s.port || 3000,
    install_command: s.install_command ?? "",
    build_command: s.build_command ?? "",
    start_command: s.start_command ?? "",
    publish_directory: s.publish_directory ?? "dist",
    ...(s.compose_path ? { compose_path: s.compose_path } : {}),
  };
}
