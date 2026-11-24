import { postVisitor } from "../demo";
import { isScenarioID } from "../scenarios";
import {
  parseVisitorIdentity, parseWarehouseFrame, parseWarehouseStatus, warehouseEvents, warehouseScenario,
  type WarehouseFrame, type WarehouseStatus,
} from "./telemetry";

export type WarehouseAction = "play" | "pause" | "stop" | "restart";
const expectedState = { play: "running", pause: "paused", stop: "stopped", restart: "paused" } as const;

export function allowsWarehouseAction(state: WarehouseFrame["state"], action: WarehouseAction): boolean {
  return action === "restart" || (state !== "stopped" && (action === "stop" || action === (state === "running" ? "pause" : "play")));
}

type PendingControl = { action: WarehouseAction; generation: string; expectedGeneration?: string };

export type WarehouseFeed = {
  phase: "connecting" | "reconnecting" | "live" | "unavailable" | "expired";
  status: Extract<WarehouseStatus, { ready: true }> | null;
  frame: WarehouseFrame | null;
  pending: WarehouseAction | null;
  controlWarning: WarehouseAction | "run_changed" | null;
};

const retryDelay = 2000;
const statusTimeout = 8000;
class VisitorExpired extends Error {}

async function request(path: "/demo/status" | "/demo/scenario/warehouse-v2" | `/demo/${WarehouseAction}`, generation: string, signal: AbortSignal): Promise<unknown> {
  const response = await postVisitor(path, generation, signal);
  if (response.status === 401 || response.status === 403) throw new VisitorExpired();
  if (!response.ok) throw new Error("Warehouse session request failed.");
  return response.json();
}

export async function openWarehouse(generation: string): Promise<void> {
  if (!/^[a-f0-9]{64}$/.test(generation)) throw new Error("Refresh the visitor session before opening the warehouse.");
  try {
    const result = parseVisitorIdentity(await request("/demo/scenario/warehouse-v2", generation, AbortSignal.timeout(65000)));
    if (result.ready && result.scenarioId === warehouseScenario && result.generation !== generation) return;
  } catch (error) {
    if (error instanceof VisitorExpired) throw new Error("Visitor session ended. Clear this site's cookies to begin a new visit.");
  }
  throw new Error("The warehouse switch was not confirmed. Refresh session status before trying again.");
}

export function subscribeWarehouse(emit: (feed: WarehouseFeed) => void, onLegacy: () => void) {
  const lifetime = new AbortController();
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stream: EventSource | undefined;
  let status: WarehouseFeed["status"] = null;
  let frame: WarehouseFrame | null = null;
  let phase: WarehouseFeed["phase"] = "connecting";
  let pending: PendingControl | null = null;
  let controlWarning: WarehouseFeed["controlWarning"] = null;

  function publish(nextPhase: WarehouseFeed["phase"]): void {
    phase = nextPhase;
    emit({ phase, status, frame, pending: pending?.action ?? null, controlWarning });
  }

  function canControl(generation: string | undefined): boolean {
    return !lifetime.signal.aborted && phase === "live" && pending === null && !!status && status.generation === generation;
  }

  async function control(action: WarehouseAction, generation: string): Promise<void> {
    if (!canControl(generation) || !status || !frame || !allowsWarehouseAction(frame.state, action)) return;
    const runtimeId = status.runtimeId;
    const attempt: PendingControl = { action, generation };
    pending = attempt;
    controlWarning = null;
    closeStream();
    publish("reconnecting");

    try {
      const timeout = AbortSignal.timeout(action === "restart" ? 65000 : statusTimeout);
      const reply = await request(`/demo/${action}`, generation, AbortSignal.any([lifetime.signal, timeout]));
      if (action === "restart") {
        const identity = parseVisitorIdentity(reply);
        if (!identity.ready || identity.scenarioId !== warehouseScenario || identity.generation === generation) {
          throw new Error("Unconfirmed restart");
        }
        attempt.expectedGeneration = identity.generation;
      } else {
        const confirmation = parseWarehouseStatus(reply);
        if (!confirmation.ready || confirmation.generation !== generation || confirmation.runtimeId !== runtimeId ||
            confirmation.state !== expectedState[action]) throw new Error("Unconfirmed simulation control");
        attempt.expectedGeneration = generation;
      }
    } catch {
      controlWarning = action;
      if (!lifetime.signal.aborted) publish("reconnecting");
    }
    // Read the current status and full snapshot even if the mutation response was lost.
    if (!lifetime.signal.aborted) void connect();
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
          if (pending?.expectedGeneration &&
              (pending.expectedGeneration !== next.generation || expectedState[pending.action] !== next.state)) {
            controlWarning = "run_changed";
          }
          pending = null;
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
      if (pending?.action === "restart" && pending.expectedGeneration && next.generation === pending.generation) {
        retry("reconnecting");
        return;
      }
      if (frame && (frame.generation !== next.generation || (next.ready && frame.runtimeId !== next.runtimeId))) frame = null;
      status = next.ready ? next : null;
      if (!next.ready) { retry("unavailable"); return; }
      publish(pending || frame ? "reconnecting" : "connecting");
      listen(next);
    } catch (error) {
      if (lifetime.signal.aborted) return;
      if (error instanceof VisitorExpired) {
        closeStream();
        status = null;
        frame = null;
        pending = null;
        publish("expired");
      } else {
        retry("reconnecting");
      }
    }
  }

  publish("connecting");
  void connect();
  return {
    canControl,
    control,
    close() {
      lifetime.abort();
      closeStream();
    },
  };
}
