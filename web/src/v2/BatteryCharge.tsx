import { Icon } from "./WarehouseIcon";

// WarehouseV2Config inherits MinAssignBattery and ChargeBelow from backend/sim/config.go.
const minAssignmentCharge = 30;
const autoChargeThreshold = 40;

export function BatteryCharge({ percent, id }: { percent: number; id?: string }) {
  const level = percent < minAssignmentCharge ? "critical" : percent <= autoChargeThreshold ? "low" : "healthy";
  return <span className="v2-card-charge" id={id} data-charge={level} role="img" aria-label={"Battery charge " + percent + "%, " + level}>
    <Icon name="battery" size={20}>
      {percent > 0 && <rect x="5" y="9" width={12 * percent / 100} height="6" rx="1" fill="currentColor" stroke="none" />}
    </Icon>
    {percent}%
  </span>;
}
