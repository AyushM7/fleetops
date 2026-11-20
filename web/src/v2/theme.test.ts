import { describe, expect, it } from "vitest";
import theme from "./theme.css?raw";

function luminance(hex: string): number {
  const channels = [0, 2, 4].map(index => Number.parseInt(hex.slice(index, index + 2), 16) / 255);
  const [red, green, blue] = channels.map(value =>
    value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4);
  return 0.2126 * red + 0.7152 * green + 0.0722 * blue;
}

function ratio(foreground: string, background: string): number {
  const value = (role: string) => {
    const hex = theme.match(new RegExp("--v2-" + role + ": #([0-9a-f]{6});"))?.[1];
    if (!hex) throw new Error("Missing color for " + role);
    return luminance(hex);
  };
  const a = value(foreground);
  const b = value(background);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

describe("Warehouse appearance", () => {
  it("keeps primary and secondary text readable across surface levels", () => {
    for (const surface of ["bg", "rail", "toolbar", "panel", "raised", "inspector", "inspector-card", "plot"]) {
      expect(ratio("text", surface), surface).toBeGreaterThanOrEqual(4.5);
      expect(ratio("muted", surface), surface).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("keeps status and focus distinct over tints and selection", () => {
    for (const status of ["moving", "charging", "waiting", "fault"]) {
      expect(ratio(status, status + "-wash"), status).toBeGreaterThanOrEqual(4.5);
      expect(ratio(status, "selected"), status).toBeGreaterThanOrEqual(4.5);
      expect(ratio("text", status + "-fill"), status).toBeGreaterThanOrEqual(4.5);
    }
    expect(ratio("selection-ink", "selected")).toBeGreaterThanOrEqual(4.5);
    expect(ratio("focus", "selected")).toBeGreaterThanOrEqual(3);
    expect(ratio("assisted", "toolbar")).toBeGreaterThanOrEqual(4.5);
    expect(ratio("disabled-ink", "disabled-fill")).toBeGreaterThanOrEqual(4.5);
  });
});
