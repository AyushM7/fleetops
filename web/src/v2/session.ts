import { postVisitor } from "../demo";
import { isScenarioID } from "../scenarios";
import {
  parseVisitorIdentity, parseWarehouseFrame, parseWarehouseStatus, warehouseEvents, warehouseScenario,
  type WarehouseFrame, type WarehouseStatus,
} from "./telemetry";

export type WarehouseFeed = {
  phase: "connecting" | "reconnecting" | "live" | "unavailable" | "expired";
  status: Extract<WarehouseStatus, { ready: true }> | null;
  frame: WarehouseFrame | null;
};

const retryDelay = 2000;
const statusTimeout = 8000;
class VisitorExpired extends Error {}

async function request(path: "/demo/status" | "/demo/scenario/warehouse-v2", generation: string, signal: AbortSignal): Promise<unknown> {
  const response = await postVisitor(path, generation, signal);
  if (response.status === 401 || response.status === 403) throw new VisitorExpired();
  if (!response.ok) throw new Error("Warehouse V2 session request failed.");
  return response.json();
}

export async function openWarehouse(generation: string): Promise<void> {
  if (!/^[a-f0-9]{64}$/.test(generation)) throw new Error("Refresh the visitor session before opening Warehouse V2.");
  try {
    const result = parseVisitorIdentity(await request("/demo/scenario/warehouse-v2", generation, AbortSignal.timeout(65000)));
    if (result.ready && result.scenarioId === warehouseScenario && result.generation !== generation) return;
  } catch (error) {
    if (error instanceof VisitorExpired) throw new Error("Visitor session ended. Clear this site's cookies to begin a new visit.");
  }
  throw new Error("Warehouse V2 switch was not confirmed. Refresh session status before trying again.");
}

export function subscribeWarehouse(emit: (feed: WarehouseFeed) => void, onLegacy: () => void): () => void {
  const lifetime = new AbortController();
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stream: EventSource | undefined;
  let status: WarehouseFeed["status"] = null;
  let frame: WarehouseFrame | null = null;

  function publish(phase: WarehouseFeed["phase"]): void {
    emit({ phase, status, frame });
  }

  function closeStream(): void {
    stream?.close();
    stream = undefined;
    clearTimeout(timer);
  }

  function retry(phase: "reconnecting" | "unavailable"): void {
    closeStream();
    publish(phase);
    timer = setTimeout(() => { void connect(); }, retryDelay);
  }

  function listen(current: NonNullable<WarehouseFeed["status"]>): void {
    // A fresh EventSource has no replay cursor, so Go starts it with its latest full snapshot.
    const source = new EventSource("/demo/events");
    stream = source;
    let reconciled = false;
    const currentSource = () => !lifetime.signal.aborted && stream === source;
    const onError = () => { if (currentSource()) retry("reconnecting"); };
    source.onerror = onError;
    timer = setTimeout(onError, statusTimeout);

    const receive = (event: MessageEvent<string>) => {
      if (!currentSource()) return;
      try {
        const next = parseWarehouseFrame(event, current);
        if (!reconciled) {
          if (next.type !== "snapshot" || next.eventId < (frame?.eventId ?? 0) ||
              next.snapshot.tick < Math.max(current.snapshot.tick, frame?.snapshot.tick ?? 0) ||
              (frame && next.eventId === frame.eventId && next.snapshot.tick !== frame.snapshot.tick)) {
            return;
          }
          reconciled = true;
        } else if (frame) {
          if (next.eventId <= frame.eventId) return;
          const expectedTick = frame.snapshot.tick + (next.type === "tick" ? 1 : 0);
          if (next.eventId !== frame.eventId + 1 || next.snapshot.tick !== expectedTick) {
            retry("reconnecting");
            return;
          }
        }
        frame = next;
        clearTimeout(timer);
        // Paused and stopped streams can legitimately send only SSE comments.
        if (next.state === "running") timer = setTimeout(onError, statusTimeout);
        publish("live");
      } catch {
        retry("reconnecting");
      }
    };
    for (const type of warehouseEvents) source.addEventListener(type, receive as EventListener);
  }

  async function connect(): Promise<void> {
    try {
      const signal = AbortSignal.any([lifetime.signal, AbortSignal.timeout(statusTimeout)]);
      const raw = await request("/demo/status", "", signal);
      if (lifetime.signal.aborted) return;
      const identity = parseVisitorIdentity(raw);
      if (isScenarioID(identity.scenarioId)) {
        closeStream();
        onLegacy();
        return;
      }
      const next = parseWarehouseStatus(raw);
      if (frame && (frame.generation !== next.generation || (next.ready && frame.runtimeId !== next.runtimeId))) frame = null;
      status = next.ready ? next : null;
      if (!next.ready) { retry("unavailable"); return; }
      publish(frame ? "reconnecting" : "connecting");
      listen(next);
    } catch (error) {
      if (lifetime.signal.aborted) return;
      if (error instanceof VisitorExpired) {
        closeStream();
        status = null;
        frame = null;
        publish("expired");
      } else {
        retry("reconnecting");
      }
    }
  }

  publish("connecting");
  void connect();
  return () => {
    lifetime.abort();
    closeStream();
  };
}
