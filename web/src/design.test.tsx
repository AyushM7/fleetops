/// <reference types="vite/client" />
import css from "./styles.css?raw";
import { renderToString } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Warehouse, RobotCard, robotDescription } from "./Warehouse";
import { Operations } from "./Operations";
import { InboxView } from "./ApprovalInbox";
import App, { Dashboard } from "./App";
import type { FleetSnapshot } from "./fleet";
import type { Proposal } from "./proposals";

const source = { id: "R1", x: 0, y: 3, status: "moving" as const, battery: 100, jobId: "J1", blocked: true, waitTicks: 42 };
const fleet: FleetSnapshot = {
  tick: 42, poisoned: false,
  layout: { width: 8, height: 8, cells: [{ x: 2, y: 0, kind: "wall" }, { x: 7, y: 0, kind: "station" }] },
  robots: [source, { id: "R2", x: 2, y: 3, status: "offline", battery: 100, jobId: "", blocked: false, waitTicks: 0 }],
  stations: [{ id: "S1", x: 7, y: 0, occupiedBy: "", reservedBy: "" }],
  activeIncidents: [{ robotId: "R1", type: "traffic_blocked", severity: "high", telemetry: source }],
  jobs: [{ id: "J1", assignedRobot: "R1", status: "assigned", fromX: 4, fromY: 4, toX: 6, toY: 4 }],
};
const proposal: Proposal = { id: "P-current", action: "reassign_unstarted_job", robotId: "", stationId: "", jobId: "J1", fromRobotId: "R1", toRobotId: "R3", state: "pending", execution: "none", submittedBy: "proposer", currentRuntime: true, createdAt: "2026-10-17T12:00:00Z", expiresAt: "2030-10-17T12:00:00Z" };
const html = (node: React.ReactNode) => renderToString(node).replace(/<!--.*?-->/g, "");
afterEach(() => vi.unstubAllGlobals());

describe("grounded dashboard design", () => {
  it("provides a keyboard-focusable main target for the skip link", () => {
    const output = html(<App />);
    expect(output).toContain('href="#main-content">Skip to warehouse');
    expect(output).toContain('<main id="main-content" tabindex="-1">');
  });
  it("exposes selectable SVG markers with full telemetry and independent affected/selected states", () => {
    const output = html(<Warehouse fleet={fleet} selectedRobotID="R1" onSelectRobot={vi.fn()} />);
    expect(output).toContain('role="group"');
    expect(output).toContain('role="button" tabindex="0"');
    expect(output).toContain('aria-pressed="true"');
    expect(output).toContain('robot-blocked robot-affected robot-selected');
    expect(output).toContain('robot robot-offline');
    expect(output).toContain('transform="translate(20,138)"');
    expect(output).toContain('class="cell cell-wall"');
    expect(output).toContain('S1: available');
    expect(output).toContain(robotDescription(source));
    expect(output).not.toMatch(/<path|animateMotion|<animate|route-points/);
  });
  it("offers equivalent telemetry buttons with pressed state and readable battery/fault information", () => {
    const selected = html(<RobotCard robot={source} selectedRobotID="R1" onSelectRobot={vi.fn()} />);
    expect(selected).toContain('class="robot-card is-selected"');
    expect(selected).toContain('aria-label="Inspect R1: moving, traffic blocked');
    expect(selected).toContain('aria-pressed="true"');
    expect(selected).toContain('aria-label="R1 battery 100%"');
    expect(selected).toContain('waiting 42 ticks');
    expect(html(<RobotCard robot={fleet.robots[1]} onSelectRobot={vi.fn()} />)).toContain("Robot unresponsive");
  });
  it("links selection to supplied incidents without filtering away other conditions or inventing resolution", () => {
    const output = html(<Operations fleet={fleet} stale={false} selectedRobotID="R1" onSelectRobot={vi.fn()} />);
    expect(output).toContain('class="incident-card is-related"');
    expect(output).toContain('aria-pressed="true">Locate R1');
    expect(output).toContain("High severity");
    expect(output).toContain("Assigned · awaiting pickup");
    expect(output).not.toContain("Resolved"); expect(output).not.toContain("Delivered");
  });
  it("derives contextual counts from the snapshot and keeps stale and poison warnings explicit", () => {
    const output = html(<Dashboard feed={{ phase: "live", snapshot: fleet, updatedAt: new Date() }} />);
    expect(output).toContain('2 robots');
    expect(output).toContain('1 active');
    expect(output).toContain('1 open jobs');
    expect(output).toContain("Routes are not supplied");
    const poisoned = html(<Dashboard feed={{ phase: "unavailable", snapshot: { ...fleet, poisoned: true }, updatedAt: new Date() }} />);
    expect(poisoned).toContain("Runtime requires reconciliation");
    expect(poisoned).toContain("positions may be stale");
    expect(poisoned).toContain("current conditions cannot be confirmed");
  });
  it("places one existing decision inbox alongside the map before secondary telemetry", () => {
    const panel = <InboxView feed={{ phase: "ready", proposals: [proposal], updatedAt: new Date() }} busy={null} receipt={null} runtimeAvailable decide={vi.fn()} refresh={vi.fn()} refreshStatus={vi.fn()} />;
    const output = html(<Dashboard feed={{ phase: "live", snapshot: fleet, updatedAt: new Date() }} decisionPanel={panel} />);
    expect(output.match(/id="approval-inbox"/g)).toHaveLength(1);
    expect(output.match(/class="approve-button"/g)).toHaveLength(1);
    expect(output.indexOf('class="decision-panel"')).toBeLessThan(output.indexOf('class="fleet-panel"'));
    expect(output.match(/data-testid="tick"/g)).toHaveLength(1);
    expect(output).not.toContain('header-tick');
    expect(output).toContain('key-free');
  });
  it("keeps supplied incidents and the human action in the same contextual rail", () => {
    const output = html(<Dashboard feed={{ phase: "live", snapshot: fleet, updatedAt: new Date() }} decisionPanel={<button>Review action</button>} />);
    const rail = output.slice(output.indexOf('<aside class="operator-context"'));
    expect(rail).toContain("R1 · Traffic blocked");
    expect(rail.indexOf("R1 · Traffic blocked")).toBeLessThan(rail.indexOf("Review action"));
    expect(rail).toContain('<details class="fleet-panel">');
    expect(rail).toContain('<details class="job-disclosure">');
    expect(output).toContain('<details class="map-guide">');
    expect(output).not.toContain('overview-heading');
  });
  it("keeps the decision region and its empty state when fleet data is loading", () => {
    const panel = <InboxView feed={{ phase: "ready", proposals: [], updatedAt: new Date() }} busy={null} receipt={null} runtimeAvailable={false} decide={vi.fn()} refresh={vi.fn()} refreshStatus={vi.fn()} />;
    const output = html(<Dashboard feed={{ phase: "loading", snapshot: null, updatedAt: null }} decisionPanel={panel} />);
    expect(output).toContain('class="decision-panel"');
    expect(output).toContain("No pending proposals in this runtime.");
    expect(output).not.toContain('class="approve-button"');
  });
  it("distinguishes local recommendation provenance and canonical proposal parameters from execution", () => {
    vi.stubGlobal("document", { querySelector: () => ({ getAttribute: () => "enabled" }) });
    const output = html(<InboxView feed={{ phase: "ready", proposals: [proposal], updatedAt: new Date() }} busy={null} receipt={null} runtimeAvailable decide={vi.fn()} refresh={vi.fn()} refreshStatus={vi.fn()} />);
    expect(output).toContain("Proposal source unavailable");
    expect(output).not.toContain("Deterministic local Mastra adapter");
    expect(output).not.toContain("Live-model result");
    expect(output).toContain("Proposal parameters · compare with fleet telemetry");
    expect(output).toContain('<dt>From robot</dt><dd>R1</dd>');
    expect(output).toContain('<dt>To robot</dt><dd>R3</dd>');
    expect(output).toContain("Human decision required");
    expect(output).not.toContain("transferred from");
  });
});

function luminance(hex: string): number {
  const channels = hex.match(/[a-f\d]{2}/gi)!.map(value => parseInt(value, 16) / 255).map(value => value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4);
  return channels[0] * .2126 + channels[1] * .7152 + channels[2] * .0722;
}

describe("visual accessibility", () => {
  const token = (name: string): string => css.match(new RegExp(`--${name}: (#[a-f0-9]{6})`))![1];
  it("keeps small text, status accents and primary-action text above WCAG AA contrast", () => {
    for (const [foreground, background] of [
      [token("text"), token("surface")], [token("muted"), token("surface")],
      [token("muted"), token("raised")], [token("accent"), token("surface")],
      [token("warning"), token("surface")], [token("critical"), token("surface")],
      ["#17181b", token("accent")],
      [token("muted"), "#0b0c0e"], [token("muted"), token("surface")],
      ["#f1c98b", "#30291f"], [token("muted"), "#141518"],
    ]) {
      const values = [luminance(foreground), luminance(background)].sort((a, b) => b - a);
      expect((values[0] + .05) / (values[1] + .05)).toBeGreaterThanOrEqual(4.5);
    }
  });
  it("uses neutral theme tokens and keeps blue/cyan out of the stylesheet", () => {
    expect(token("surface")).toBe("#141518");
    expect(token("raised")).toBe("#202124");
    expect(token("text")).toBe("#f5f5f5");
    expect(token("muted")).toBe("#b9bbc1");
    expect(token("accent")).toBe("#e8e9eb");
    for (const color of css.match(/#[a-f0-9]{6}\b/gi) ?? []) {
      const [r, g, b] = color.slice(1).match(/../g)!.map(channel => parseInt(channel, 16));
      if (color !== token("healthy")) expect(b - r).toBeLessThanOrEqual(12);
      if (color !== token("healthy")) expect(g - r).toBeLessThanOrEqual(12);
    }
    expect(css).toContain("outline: 2px solid var(--accent)");
    expect(css).toContain("border-left: 2px solid var(--warning)");
    expect(css).toContain(".connection-badge.connected { color: var(--healthy)");
  });
  it("keeps neutral walls, station boundaries and robot labels distinguishable", () => {
    const value = (selector: string, property: string): string => {
      const start = css.indexOf(selector + " {");
      return css.slice(start, css.indexOf("}", start)).match(new RegExp(property + ": (#[a-f0-9]{6})"))![1];
    };
    for (const [foreground, background, minimum] of [
      [value(".cell-wall", "fill"), value(".cell", "fill"), 3],
      [value(".cell-station", "stroke"), value(".cell-station", "fill"), 3],
      ["#ffffff", value(".robot-moving .robot-body", "fill"), 4.5],
    ] as const) {
      const values = [luminance(foreground), luminance(background)].sort((a, b) => b - a);
      expect((values[0] + .05) / (values[1] + .05)).toBeGreaterThanOrEqual(minimum);
    }
    expect(css).toContain("background: #0b0c0e");
  });
  it("matches floor, aisle, wall, station and moving swatches to the rendered map", () => {
    const rule = (selector: string): string => css.slice(css.indexOf(selector + " {"), css.indexOf("}", css.indexOf(selector + " {")));
    const color = (selector: string, property: string): string => rule(selector).match(new RegExp(property + ": (#[a-f0-9]{6})"))![1];
    for (const [cell, key] of [[".cell", ".key-free"], [".cell-aisle", ".key-aisle"], [".cell-wall", ".key-wall"], [".cell-station", ".key-station"], [".robot-moving .robot-body", ".key-moving"]]) {
      expect(color(key, "background")).toBe(color(cell, "fill"));
    }
    expect(rule(".key-offline")).toContain("dashed var(--critical)");
    expect(rule(".key-blocked")).toContain("2px solid var(--warning)");
  });
  it("disables transitions and animations for reduced motion and never interpolates map positions", () => {
    expect(css).toContain("prefers-reduced-motion: reduce");
    expect(css).toContain("transition: none !important");
    expect(css).toContain("animation: none !important");
    expect(css).not.toMatch(/transition:[^;}]*\b(transform|all|x|y)\b/);
  });
});
