import type { Point, WarehouseFrame } from "./telemetry";

export type RobotSample = { frame: WarehouseFrame; point: Point; receivedAt: number; live: boolean };

export function robotGlide(previous: RobotSample | null, current: RobotSample) {
  if (!previous?.live || !current.live) return null;
  const { frame: from, point: start } = previous;
  const { frame: to, point: end } = current;
  if (from.state !== "running" || to.state !== "running" || to.type !== "tick" ||
      from.generation !== to.generation || from.runtimeId !== to.runtimeId ||
      to.eventId !== from.eventId + 1 || to.snapshot.tick !== from.snapshot.tick + 1) return null;

  const x = start.x - end.x, y = start.y - end.y;
  const elapsed = current.receivedAt - previous.receivedAt;
  // Skip batched deliveries and gaps. Only animate the edge the simulator confirmed.
  if (Math.abs(x) + Math.abs(y) !== 1 || elapsed < 70 || elapsed > 2000) return null;
  return { x, y, duration: Math.min(900, elapsed * .82) };
}
