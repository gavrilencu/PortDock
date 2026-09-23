import { useState } from "react";
import { Network } from "lucide-react";
import { ContainerDrawer, TopologyGraph, useTopology, type TopoContainer, type TopoResource } from "../pages/Topology";
import { Empty, Loading } from "./ui";

/** Topology of a single resource (used on the application page). */
export function ResourceTopology({ kind, id }: { kind: TopoResource["kind"]; id: number }) {
  const topo = useTopology({ kind, id });
  const [sel, setSel] = useState<{ r: TopoResource; c: TopoContainer } | null>(null);
  if (topo.isLoading) return <Loading />;
  const list = topo.data?.resources ?? [];
  if (!list.length) return <Empty icon={<Network />} title="No topology yet" description="Deploy the application to see its containers." />;
  const live = sel && list[0];
  const liveC = live?.containers.find((c) => c.id === sel?.c.id);
  return (
    <>
      <TopologyGraph resources={list} selected={sel?.c.id} onSelect={(r, c) => setSel({ r, c })} />
      {sel && <ContainerDrawer key={sel.c.id} r={live ?? sel.r} c={liveC ?? sel.c} onClose={() => setSel(null)} />}
    </>
  );
}
