import type { ZodIssue } from "zod";
import { GroundingError } from "./reasoning.js";
import { candidateSchema, jobSchema, robotSchema, snapshotReferenceSchema } from "./contracts.js";
import { candidateEvidenceSchema, candidateKeySchema, comparisonSchema, recoveryDecisionSchema } from "./decision-contracts.js";

type EvidenceTool = "readIncident" | "listRecoveryCandidates" | "readRobots" | "readJobs";
export interface GroundingDiagnostic {
  stage: "snapshot" | "investigation" | "schema" | "observation" | "incident" | "candidate" | "comparison" | "decision";
  fieldPath: string;
  category: "schema_mismatch" | "identity_mismatch" | "fact_mismatch" | "evidence_missing" | "duplicate_evidence" | "constraint_violation" | "unclassified_grounding";
  expected: string;
  actual: string;
  toolEvidenceReference: EvidenceTool | null;
}
export interface GroundingFailure extends GroundingDiagnostic {
  phase: "before_finalization" | "after_finalization";
  requestOrdinal: number;
  toolEventOrdinals: number[];
}
export class RecoveryGroundingError extends GroundingError {
  readonly diagnostic: GroundingDiagnostic;
  readonly failure?: GroundingFailure;

  constructor(diagnostic: GroundingDiagnostic, failure?: GroundingFailure) {
    super();
    this.diagnostic = diagnostic;
    this.failure = failure;
  }
}

function describeValue(value: unknown): string {
  if (value === undefined) return "missing";
  if (value === null) return "null";
  if (Array.isArray(value)) return "array (redacted)";
  return typeof value + " (redacted)";
}

export function requireGrounded(condition: boolean, stage: GroundingDiagnostic["stage"], fieldPath: string,
  category: GroundingDiagnostic["category"], expected: string, actual: unknown, toolEvidenceReference: EvidenceTool | null = null): asserts condition {
  if (!condition) throw new RecoveryGroundingError({ stage, fieldPath, category, expected, actual: describeValue(actual), toolEvidenceReference });
}

const knownFields = new Set([
  ...[recoveryDecisionSchema, candidateEvidenceSchema, candidateKeySchema, comparisonSchema, candidateSchema,
    jobSchema, robotSchema, snapshotReferenceSchema].flatMap(schema => Object.keys(schema.shape)),
  "reference", "severity", "telemetry", "kind", "selected", "basis", "reason", "relevantIncidents"
]);

function schemaConstraint(issue: ZodIssue): string {
  switch (issue.code) {
    case "invalid_type": return "has schema type " + issue.expected;
    case "unrecognized_keys": return "contains only declared fields";
    case "invalid_value": return "matches a declared literal or enum value";
    case "too_big": return "does not exceed the schema maximum";
    case "too_small": return "meets the schema minimum";
    default: return "matches the declared recovery-decision-v2 schema at this path";
  }
}

export function schemaFailure(issue: ZodIssue, input: unknown): never {
  let fieldPath = "$";
  let actual = input;
  for (const p of issue.path) {
    if (typeof p === "number" && Number.isSafeInteger(p) && p >= 0) fieldPath += "[" + p + "]";
    else if (typeof p === "string" && knownFields.has(p)) fieldPath += (fieldPath === "$" ? "" : ".") + p;
    else break;
    actual = actual !== null && typeof actual === "object" && Object.hasOwn(actual, p)
      ? (actual as Record<PropertyKey, unknown>)[p] : undefined;
  }
  throw new RecoveryGroundingError({
    stage: "schema", fieldPath: fieldPath.replace(/^\$(?=[a-zA-Z])/, ""), category: "schema_mismatch",
    expected: schemaConstraint(issue), actual: describeValue(actual), toolEvidenceReference: null
  });
}
