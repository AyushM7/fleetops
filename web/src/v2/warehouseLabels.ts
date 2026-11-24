import type { Point, Robot, Station } from "./telemetry";
import { robotTone } from "./warehousePresentation";

export type Camera = Point & { scale: number };
type Viewport = { width: number; height: number };
type Box = Point & Viewport;
export type MapLabel = Box & { font: number };
export const mapRobotLabel = (robot: Pick<Robot, "id" | "role">): string =>
  robot.role === "maintenance" ? "M" : robot.id.replace(/^R(?=\d+$)/, "");
type Pin = { anchor: Point; size: number };

function screenPoint(point: Point, camera: Camera): Point {
  return {
    x: camera.x + (point.x + 0.5) * camera.scale,
    y: camera.y + (point.y + 0.5) * camera.scale,
  };
}

export function mapTargetVisible(point: Point, camera: Camera, viewport: Viewport): boolean {
  const { x, y } = screenPoint(point, camera);
  return x >= 6 && x <= viewport.width - 6 && y >= 6 && y <= viewport.height - 6;
}

function overlaps(a: Box, b: Box): boolean {
  return Math.abs(a.x - b.x) < (a.width + b.width) / 2 + 1 &&
    Math.abs(a.y - b.y) < (a.height + b.height) / 2 + 1;
}

function nearbySlots(dx: number, dy: number): Point[] {
  return [
    { x: 0, y: dy }, { x: dx, y: 0 }, { x: -dx, y: 0 }, { x: 0, y: -dy },
    { x: dx, y: dy }, { x: -dx, y: dy }, { x: dx, y: -dy }, { x: -dx, y: -dy },
  ];
}

export function placeMapLabels(
  robots: Robot[], stations: Station[], camera: Camera, viewport: Viewport, selectedId?: string, pins: readonly Pin[] = [],
): Map<string, MapLabel> {
  const { scale } = camera;
  const entities = [
    ...stations.map(station => ({ ...station, text: station.id, charger: true, glyph: false, width: .8, height: .8, priority: 0 })),
    ...robots.map(robot => ({ ...robot, text: mapRobotLabel(robot), charger: false, glyph: !!robotTone(robot),
      width: robot.role === "maintenance" ? .9 : .84, height: .68,
      priority: robot.id === selectedId ? 0 : robotTone(robot) ? 1 : 2 })),
  ];
  const anchors = entities.map(entity => {
    const center = { ...screenPoint(entity, camera), width: entity.width * scale, height: entity.height * scale };
    const glyph = { x: center.x + .37 * scale, y: center.y - .34 * scale, width: .31 * scale, height: .29 * scale };
    return { ...entity, center, obstacles: entity.glyph ? [center, glyph] : [center] };
  });
  const occupied: Box[] = pins.map(({ anchor, size }) => ({
    ...screenPoint(anchor, camera), width: (size * 2 + .05) * scale, height: (size * 2 + .05) * scale,
  }));
  const labels = new Map<string, MapLabel>();
  const robotAnchors = anchors.filter(anchor => !anchor.charger).sort((a, b) => a.priority - b.priority);

  function place(anchor: typeof anchors[number], centeredOnly: boolean): void {
    const { id, x, y, charger, center } = anchor;
    if (!mapTargetVisible(anchor, camera, viewport)) return;
    const font = Math.max(11, Math.min(charger ? 16 : 20, scale * (charger ? .34 : .42)));
    const width = Math.max(charger ? 0 : center.width, anchor.text.length * font * .64 + 4);
    const height = Math.max(charger ? 0 : center.height, font + 3);
    const dx = (center.width + width) / 2 + 2;
    const dy = (center.height + height) / 2 + 2;
    const offsets = centeredOnly ? [{ x: 0, y: 0 }]
      : [...nearbySlots(dx, dy), { x: 0, y: dy + height + 2 }, { x: 0, y: -dy - height - 2 },
        { x: dx + width + 2, y: 0 }, { x: -dx - width - 2, y: 0 }];
    for (const offset of offsets) {
      const box = { x: center.x + offset.x, y: center.y + offset.y, width, height };
      if (box.x - width / 2 < 1 || box.x + width / 2 > viewport.width - 1 ||
          box.y - height / 2 < 1 || box.y + height / 2 > viewport.height - 1) continue;
      if (occupied.some(other => overlaps(box, other))) continue;
      if (anchors.some(other => (other.x !== x || other.y !== y) &&
          other.obstacles.some(footprint => overlaps(box, footprint)))) continue;
      occupied.push(box);
      labels.set(id, { x: offset.x / scale, y: offset.y / scale, width: width / scale, height: height / scale, font: font / scale });
      return;
    }
  }

  // Keep names on their markers where they fit before reserving space for captions and displaced names.
  robotAnchors.forEach(anchor => place(anchor, true));
  anchors.filter(anchor => anchor.charger).forEach(anchor => place(anchor, false));
  robotAnchors.filter(anchor => !labels.has(anchor.id)).forEach(anchor => place(anchor, false));
  return labels;
}
