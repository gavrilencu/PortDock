import { lazy, Suspense } from "react";
import { Loading } from "./ui";

const Impl = lazy(() => import("./Terminal").then((m) => ({ default: m.Terminal })));

/** The web terminal, with xterm.js loaded only when a terminal is opened. */
export function Terminal(props: { path: string; className?: string }) {
  return (
    <Suspense fallback={<Loading />}>
      <Impl {...props} />
    </Suspense>
  );
}
