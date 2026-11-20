import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { BatteryCharge } from "./BatteryCharge";

describe("Warehouse V2 battery charge", () => {
  it.each([
    [0, "critical"], [29, "critical"], [30, "low"], [40, "low"], [41, "healthy"], [100, "healthy"],
  ])("describes %i%% as %s at the Go policy boundaries", (percent, level) => {
    const html = renderToStaticMarkup(<BatteryCharge percent={percent} />);
    expect(html).toContain('data-charge="' + level + '"');
    expect(html).toContain('aria-label="Battery charge ' + percent + '%, ' + level + '"');
  });

  it("fills the existing battery icon proportionally and leaves 0% empty", () => {
    const charge = (percent: number) => renderToStaticMarkup(<BatteryCharge percent={percent} />);
    expect(charge(0)).not.toContain('fill="currentColor"');
    expect(charge(50)).toContain('width="6" height="6" rx="1" fill="currentColor"');
    expect(charge(100)).toContain('width="12" height="6" rx="1" fill="currentColor"');
  });
});
