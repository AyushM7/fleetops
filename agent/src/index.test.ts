import { describe, it, expect } from "vitest";
import { greet } from "./index.js";

describe("greet", () => {
  it("includes the supplied name in the greeting", () => {
    expect(greet("Ada")).toBe("Hello, Ada!");
    expect(greet("Grace")).toBe("Hello, Grace!");
  });
});
