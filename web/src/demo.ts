// Only the isolated local gateway inserts this marker into the served HTML.
export function isLocalDemo(): boolean {
  return typeof document !== "undefined" && document.querySelector('meta[name="fleetops-local-demo"]')?.getAttribute("content") === "enabled";
}

export function authentication(token: string): Pick<RequestInit, "credentials" | "headers"> {
  return isLocalDemo()
    ? { credentials: "same-origin", headers: { "Content-Type": "application/json", "X-FleetOps-Demo": "1", ...(token && token !== "local-demo-session" ? { "X-FleetOps-Generation": token } : {}) } }
    : { credentials: "omit", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` } };
}
