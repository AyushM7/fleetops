import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { validateSnapshot } from "../../agent/src/reasoning.js";
import { dataset, type Scenario } from "./dataset.js";

// Labels are reviewed independently of the model and observer.
const labels = [
  ["blocked-reassignment", "traffic-eligible"], ["blocked-no-candidate", "traffic-no-candidate"],
  ["routine-charging", "battery-routine-charge"], ["healthy-operations", "healthy-no-action"],
] as const;

export function demo08Scenarios(path: string): Scenario[] {
  const raw: unknown = JSON.parse(readFileSync(path, "utf8"));
  if (!Array.isArray(raw) || raw.length !== labels.length) throw new Error("Four Go demo exports required.");
  return labels.map(([id, label], index) => {
    const row = raw[index] as Record<string, unknown>;
    if (!row || row.id !== id || typeof row.source !== "string" || !row.source.startsWith("backend/fleet/demo_export_test.go NewDemoScenarioService;") ||
        typeof row.runtimeId !== "string" || !/^P[a-f0-9]{32}$/.test(row.runtimeId) ||
        row.sha256 !== createHash("sha256").update(JSON.stringify(row.snapshot)).digest("hex")) throw new Error("Go export identity/hash mismatch.");
    const input = validateSnapshot(row.snapshot);
    const base = dataset().find(s => s.id === label)!;
    // Incident truth is Go's classifier; the action label is frozen above.
    return { ...base, id, description: `Actual DEMO-09 ${id} starting observation.`, input,
      inputKind: "existing-tested-fixture", provenance: `${row.source}; runtime=${row.runtimeId}; snapshotSHA256=${row.sha256}`,
      expected: { action: structuredClone(base.expected.action), incidents: input.activeIncidents.map(i => ({ scope: "snapshot", tick: input.tick, robotId: i.robotId, type: i.type })) },
      expectedScoring: { status: "passed", checks: { ...base.expectedScoring.checks,
        operatorAttention: input.activeIncidents.length ? "pass" : "not_applicable" } } };
  });
}
