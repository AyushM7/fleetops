import type { Point, Robot, Station } from "./telemetry";

export type Camera = Point & { scale: number };
type Viewport = { width: number; height: number };
type Box = Point & Viewport;
export type MapLabel = Box & { font: number };

export function mapTargetVisible(point: Point, camera: Camera, viewport: Viewport): boolean {
  const x = camera.x + (point.x + 0.5) * camera.scale;
  const y = camera.y + (point.y + 0.5) * camera.scale;
  return x >= 6 && x <= viewport.width - 6 && y >= 6 && y <= viewport.height - 6;
}

function overlaps(a: Box, b: Box): boolean {
  return Math.abs(a.x - b.x) < (a.width + b.width) / 2 + 1 &&
    Math.abs(a.y - b.y) < (a.height + b.height) / 2 + 1;
}

export function placeMapLabels(robots: Robot[], stations: Station[], camera: Camera, viewport: Viewport): Map<string, MapLabel> {
  const { scale } = camera;
  const anchors = [
    ...stations.map(station => ({ ...station, charger: true, width: .8, height: .8 })),
    ...robots.map(robot => ({ ...robot, charger: false, width: robot.role === "maintenance" ? .84 : .76, height: .58 })),
  ].map(anchor => ({ ...anchor, center: {
    x: camera.x + (anchor.x + .5) * scale,
    y: camera.y + (anchor.y + .5) * scale,
    width: anchor.width * scale, height: anchor.height * scale,
  } }));
  const occupied: Box[] = [];
  const labels = new Map<string, MapLabel>();

  for (const { id, x, y, charger, center } of anchors) {
    if (!mapTargetVisible({ x, y }, camera, viewport)) continue;
    const font = Math.max(11, Math.min(16, scale * (charger ? .34 : .37)));
    const width = Math.max(charger ? 0 : center.width, id.length * font * .64 + 4);
    const height = Math.max(charger ? 0 : center.height, font + 3);
    const dx = (center.width + width) / 2 + 2;
    const dy = (center.height + height) / 2 + 2;
    const offsets = [
      ...(!charger ? [{ x: 0, y: 0 }] : []),
      { x: 0, y: dy }, { x: dx, y: 0 }, { x: -dx, y: 0 }, { x: 0, y: -dy },
      { x: dx, y: dy }, { x: -dx, y: dy }, { x: dx, y: -dy }, { x: -dx, y: -dy },
    ];
    for (const offset of offsets) {
      const box = { x: center.x + offset.x, y: center.y + offset.y, width, height };
      if (box.x - width / 2 < 1 || box.x + width / 2 > viewport.width - 1 ||
          box.y - height / 2 < 1 || box.y + height / 2 > viewport.height - 1) continue;
      if (occupied.some(other => overlaps(box, other))) continue;
      if (anchors.some(other => other.id !== id && (other.x !== x || other.y !== y) && overlaps(box, other.center))) continue;
      occupied.push(box);
      labels.set(id, { x: offset.x / scale, y: offset.y / scale, width: width / scale, height: height / scale, font: font / scale });
      break;
    }
  }
  return labels;
}
