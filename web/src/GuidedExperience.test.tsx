import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToString } from "react-dom/server";
import GuidedExperience, { guidedMessage } from "./GuidedExperience";
import SimulationControls from "./SimulationControls";
import { pollDemoStatus, type DemoStatus, type DemoGuidance } from "./restart";

const initial: DemoStatus = { generation: "a".repeat(64), ready:true, state:"paused", speed:"1", guide:{stage:"observe"}, snapshot:{tick:0,poisoned:false,layout:{width:1,height:1,cells:[{x:0,y:0,kind:"free"}]},robots:[],stations:[],activeIncidents:[],jobs:[]} };
afterEach(()=>{vi.unstubAllGlobals();vi.useRealTimers();});

describe("guided walkthrough",()=>{
  it("states the actual initialization and provides accessible optional facts without an invented fault onset",()=>{
    const html=renderToString(<GuidedExperience status={initial} unavailable={false}/>);
    expect(html).toContain('aria-current="step"');expect(html).toContain("already offline");expect(html).toContain("from tick 0");expect(html).toContain("Scenario facts");expect(html).not.toContain("went offline");
    expect(renderToString(<GuidedExperience status={{...initial,guide:undefined}} unavailable={false}/>)).toBe("");
  });
  it("grounds incident, analyzing, expired, unavailable and rejected guidance in status",()=>{
    const message=(guide:DemoGuidance)=>guidedMessage({...initial,guide});
    expect(message({stage:"incident"})).toContain("Go reports traffic blockage");
    expect(message({stage:"analyze",decisionTick:42})).toContain("deterministic local adapter");
    expect(message({stage:"decide",decision:"expired"})).toContain("wall-clock");
    expect(message({stage:"outcome",decision:"rejected"})).toContain("no reassignment executed");
    expect(renderToString(<GuidedExperience status={initial} unavailable/>)).toContain("current state is unconfirmed");
    expect(guidedMessage({...initial,ready:false})).toContain("unconfirmed proposal outcome");
  });
  it("never labels a transfer as delivery and only reports a measured authoritative completion",()=>{
    const transferred:DemoStatus={...initial,guide:{stage:"outcome",decision:"approved",decisionTick:42}};
    expect(guidedMessage(transferred)).toContain("delivery is not confirmed");
    const complete:DemoStatus={...transferred,guide:{...transferred.guide!,deliveryTick:45},snapshot:{...initial.snapshot!,tick:45,jobs:[{id:"J1",status:"done",assignedRobot:"R3",fromX:0,fromY:0,toX:0,toY:0}]}};
    expect(guidedMessage(complete)).toContain("tick 45, 3 simulator ticks");
    expect(guidedMessage({...complete,snapshot:{...complete.snapshot!,jobs:[]}})).toContain("delivery is not confirmed");
  });
  it("allows lead-up ticking but blocks both tick operations during real proposal preparation",()=>{
    const render=(status:DemoStatus)=>renderToString(<SimulationControls status={status} busy={false} unavailable={false} error={null} onControl={()=>{}}/>);
    expect(render(initial)).not.toContain('disabled=""');
    const analyzing=render({...initial,state:"preparing",guide:{stage:"analyze"}});
    expect(analyzing).toContain("Preparing recommendation");expect(analyzing.match(/disabled=""/g)).toHaveLength(2);
  });
  it("validates guidance and rejects invented stages, identities, future ticks or unapproved delivery",async()=>{
    vi.useFakeTimers();
    for(const guide of [{stage:"fake"},{stage:"decide",proposalId:"bad"},{stage:"outcome",decision:"rejected",decisionTick:0,deliveryTick:0},{stage:"analyze",decisionTick:42}]) {
      vi.stubGlobal("fetch",vi.fn().mockResolvedValue(new Response(JSON.stringify({...initial,guide}))));
      const emit=vi.fn();const stop=pollDemoStatus(emit,vi.fn());await vi.advanceTimersByTimeAsync(0);expect(emit.mock.calls[0][0]).toBeNull();stop();
    }
    vi.stubGlobal("fetch",vi.fn().mockResolvedValue(new Response(JSON.stringify(initial))));
    const emit=vi.fn();const stop=pollDemoStatus(emit,vi.fn());await vi.advanceTimersByTimeAsync(0);expect(emit.mock.calls[0][0].guide.stage).toBe("observe");stop();
  });
});
