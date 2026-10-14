import { renderToString } from "react-dom/server";
import { describe, it, expect } from "vitest";
import App from "./App";

describe("App", () => {
  it("renders the project name and current status", () => {
    const html = renderToString(<App />);
    expect(html).toContain("FleetOps");
    expect(html).toContain("Status: in progress");
  });
});
