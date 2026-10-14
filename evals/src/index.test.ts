import { describe, it, expect } from "vitest";
import { double } from "./index.js";

describe("double", () => {
  it("doubles positive, negative, and zero values", () => {
    expect(double(3)).toBe(6);
    expect(double(-4)).toBe(-8);
    expect(double(0)).toBe(0);
  });
});
