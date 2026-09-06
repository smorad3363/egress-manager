import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";

const liveRoute = {
  id: "office_vpn",
  name: "Office VPN",
  source: { kind: "subnet", subnet: "10.20.0.0/16" },
  outbound_id: "wg_primary",
  failure_policy: "block",
  dns_policy: "follow_outbound",
  dns_servers: ["1.1.1.1"],
  ipv4_policy: "follow_outbound",
  ipv6_policy: "block",
  kill_switch: true,
  mtu: 1400,
  tcp_mss: 1360,
  enabled: true,
};

const liveOutbound = {
  id: "wg_primary",
  name: "Primary WireGuard",
  adapter: "sing-box",
  type: "wireguard",
  server: { host: "192.0.2.20", port: 51820 },
  capabilities: { tcp: true, udp: true },
  enabled: true,
  health: {
    status: "healthy",
    configuration_valid: "passed",
    transport_reachable: "passed",
    internet_reachable: "passed",
    external_ip: "203.0.113.20",
    tcp: "passed",
    udp: "passed",
  },
  secret_metadata: [],
};

const liveXrayOutbound = {
  id: "xray_reality",
  name: "Reality XHTTP",
  adapter: "xray",
  type: "vless",
  server: { host: "198.51.100.30", port: 443 },
  capabilities: { tcp: true, udp: true },
  enabled: true,
  health: {
    status: "healthy",
    configuration_valid: "passed",
    transport_reachable: "passed",
    internet_reachable: "passed",
    external_ip: "203.0.113.31",
    tcp: "passed",
    udp: "passed",
  },
  secret_metadata: ["uuid", "public_key", "short_id"],
};

const liveRelay = {
  id: "ssh_gateway",
  name: "SSH gateway",
  listen_address: "0.0.0.0",
  listen_port: 6111,
  network: "tcp",
  destination: { host: "91.107.220.12", port: 6111 },
  outbound_id: "xray_reality",
  source_cidrs: ["198.51.100.0/24"],
  enabled: true,
};

function storedOutbounds() {
  return [
    { outbound: liveOutbound, revision: 2, created_at: "2026-09-06T12:00:00Z", updated_at: "2026-09-06T12:00:00Z" },
    { outbound: liveXrayOutbound, revision: 3, created_at: "2026-09-06T12:00:00Z", updated_at: "2026-09-06T12:00:00Z" },
  ];
}

async function mockLiveDashboard(page: Page) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/v1/control/health") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ status: "ok", version: "v0.1.0-alpha.9", checks: { database: "ok", egressd: "ok" } }) });
      return;
    }
    if (url.pathname === "/api/v1/routes") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [{ route: liveRoute, revision: 4 }], next_cursor: "" }) });
      return;
    }
    if (url.pathname === "/api/v1/outbounds") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: storedOutbounds(), next_cursor: "" }) });
      return;
    }
    if (url.pathname === "/api/v1/relays") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [{ relay: liveRelay, revision: 2 }], next_cursor: "" }) });
      return;
    }
    if (url.pathname === "/api/v1/port-forwards/counters") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [{ forward_id: "web", accepted_packets: 120, accepted_bytes: 8192, dropped_packets: 3, dropped_bytes: 192 }] }) });
      return;
    }
    if (url.pathname === "/api/v1/port-forwards") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [{ forward: { id: "web", name: "Web", enabled: true }, revision: 1 }], next_cursor: "" }) });
      return;
    }
    if (url.pathname === "/api/v1/haproxy/stats") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ info: { version: "3.0", pid: 4321 }, stats: [] }) });
      return;
    }
    if (url.pathname === "/api/v1/xray/discovery") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ installations: [], warnings: [] }) });
      return;
    }
    if (url.pathname === "/api/v1/xray/bindings") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [], next_cursor: "" }) });
      return;
    }
    await route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ message: "not mocked" }) });
  });
}

test("dashboard uses live API data and only exposes finished pages", async ({ page }) => {
  await mockLiveDashboard(page);
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Current server overview" })).toBeVisible();
  await expect(page.getByText("Office VPN").first()).toBeVisible();
  await expect(page.getByText("Primary WireGuard").first()).toBeVisible();
  await expect(page.getByText("SSH gateway").first()).toBeVisible();
  await expect(page.getByText("EU applications")).toHaveCount(0);
  await expect(page.getByText("Frankfurt · WG0")).toHaveCount(0);
  await expect(page.getByText("4.6 TB")).toHaveCount(0);
  await expect(page.getByText("99.98%")).toHaveCount(0);

  const navigation = page.getByLabel("Primary navigation");
  await expect(navigation.getByRole("button")).toHaveCount(8);
  await expect(navigation.getByRole("button", { name: "Relays" })).toBeVisible();
  await expect(navigation.getByRole("button", { name: "Firewall" })).toHaveCount(0);
  await expect(navigation.getByRole("button", { name: "Logs" })).toHaveCount(0);
  await expect(navigation.getByRole("button", { name: "Settings" })).toHaveCount(0);

  await page.locator(".routes-card").getByRole("button", { name: /View all/ }).click();
  await expect(page.getByRole("heading", { name: "Interface & subnet routes" })).toBeVisible();

  await navigation.getByRole("button", { name: "Dashboard" }).click();
  await page.locator(".topbar").getByRole("button", { name: "New route" }).click();
  await expect(page.getByRole("dialog").getByRole("heading", { name: "Create egress route" })).toBeVisible();
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();

  await page.getByRole("search").getByPlaceholder("Search").fill("relay");
  await page.getByRole("search").getByPlaceholder("Search").press("Enter");
  await expect(page.getByRole("heading", { name: "Listener relays" })).toBeVisible();

  await page.getByRole("search").getByPlaceholder("Search").fill("Xray");
  await page.getByRole("search").getByPlaceholder("Search").press("Enter");
  await expect(page.getByRole("heading", { name: "Native Xray routing" })).toBeVisible();
  await expect(page.locator(".topbar").getByRole("button", { name: "New binding" })).toHaveCount(0);
});

test("listener relay console performs CRUD and exact plan/apply without exposing credentials", async ({ page }) => {
  await page.addInitScript(() => window.sessionStorage.setItem("egress.csrf", "relay-csrf"));
  let items = [{ relay: { ...liveRelay }, revision: 2 }];
  let applyBody: Record<string, unknown> | null = null;

  await page.route("**/api/v1/outbounds?limit=128", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: storedOutbounds(), next_cursor: "" }) });
  });
  await page.route("**/api/v1/relays**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() !== "GET") expect(request.headers()["x-csrf-token"]).toBe("relay-csrf");
    if (url.pathname.endsWith("/plan")) {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({
        engine: "xray-relay",
        state_hash: "state-hash-123",
        candidate_hash: "candidate-hash-456",
        candidate_exists: true,
        enabled_relays: items.filter((item) => item.relay.enabled).length,
        enabled_outbounds: 1,
        actions: [{ kind: "replace", resource: "xray-relay", summary: "Atomically replace the project-owned Xray relay runtime." }],
      }) });
      return;
    }
    if (url.pathname.endsWith("/apply")) {
      applyBody = request.postDataJSON() as Record<string, unknown>;
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ transaction_id: "relay_apply_test", state: "COMMITTED", candidate_hash: "candidate-hash-456" }) });
      return;
    }
    if (request.method() === "POST") {
      const relay = request.postDataJSON();
      items = [...items, { relay, revision: 1 }];
      await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ relay, revision: 1 }) });
      return;
    }
    if (request.method() === "PUT") {
      const body = request.postDataJSON();
      items = items.map((item) => item.relay.id === body.relay.id ? { relay: body.relay, revision: item.revision + 1 } : item);
      const stored = items.find((item) => item.relay.id === body.relay.id)!;
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(stored) });
      return;
    }
    if (request.method() === "DELETE") {
      const body = request.postDataJSON();
      items = items.filter((item) => item.relay.id !== body.id);
      await route.fulfill({ status: 204, body: "" });
      return;
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items, next_cursor: "" }) });
  });

  await page.goto("/");
  await page.getByLabel("Primary navigation").getByRole("button", { name: "Relays" }).click();
  await expect(page.getByRole("heading", { name: "Listener relays" })).toBeVisible();
  await expect(page.getByText("91.107.220.12:6111")).toBeVisible();
  await expect(page.getByText("Reality XHTTP").first()).toBeVisible();
  await expect(page.getByText("11111111-2222-4333-8444-555555555555")).toHaveCount(0);

  await page.locator(".topbar").getByRole("button", { name: "New relay" }).click();
  let dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "Create listener relay" })).toBeVisible();
  await dialog.getByLabel("Relay ID").fill("web_gateway");
  await dialog.getByLabel("Display name").fill("Web gateway");
  await dialog.getByLabel("Listen port").fill("7443");
  await dialog.getByLabel("Selected outbound").selectOption("xray_reality");
  await dialog.getByLabel("Destination host").fill("192.0.2.44");
  await dialog.getByLabel("Destination port").fill("443");
  await dialog.getByLabel("Source CIDRs").fill("203.0.113.0/24");
  await dialog.getByRole("button", { name: "Save relay" }).click();
  await expect(page.getByText("Web gateway")).toBeVisible();

  const createdCard = page.locator(".outbound-card").filter({ hasText: "Web gateway" });
  await createdCard.getByRole("button", { name: "Edit" }).click();
  dialog = page.getByRole("dialog");
  await dialog.getByLabel("Display name").fill("Web gateway updated");
  await dialog.getByRole("button", { name: "Save relay" }).click();
  await expect(page.getByText("Web gateway updated")).toBeVisible();

  const updatedCard = page.locator(".outbound-card").filter({ hasText: "Web gateway updated" });
  await updatedCard.getByRole("button", { name: "Disable" }).click();
  await expect(updatedCard.getByText("Disabled")).toBeVisible();

  await page.getByRole("button", { name: "Review & apply" }).click();
  dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "Review listener relay plan" })).toBeVisible();
  await expect(dialog.getByText("candidate-ha…", { exact: true })).toBeVisible();
  await expect(dialog.getByText("11111111-2222-4333-8444-555555555555")).toHaveCount(0);
  await dialog.getByRole("button", { name: "Apply atomically" }).click();
  expect(applyBody).toEqual({ expected_state_hash: "state-hash-123", expected_candidate_hash: "candidate-hash-456" });

  page.once("dialog", (confirm) => confirm.accept());
  await updatedCard.getByRole("button", { name: "Delete" }).click();
  await expect(page.getByText("Web gateway updated")).toHaveCount(0);
});

test("network inventory retry and refresh perform new requests", async ({ page }) => {
  let calls = 0;
  let allowSuccess = false;
  await page.route("**/api/v1/network/inventory", async (route) => {
    calls += 1;
    if (!allowSuccess) {
      await route.fulfill({ status: 503, contentType: "application/json", body: "{}" });
      return;
    }
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        generated_at: "2026-09-06T12:00:00Z",
        interfaces: [{ name: "eth0", state: "up", mtu: 1500, addresses: [{ family: "inet", cidr: "192.0.2.9/24", scope: "global" }] }],
        routes: [{ destination: "default", gateway: "192.0.2.1", interface: "eth0", default: true }],
        listeners: [],
        dns: { source: "/etc/resolv.conf", servers: ["1.1.1.1"], search_domains: [] },
        capabilities: [],
        warnings: [],
      }),
    });
  });

  await page.goto("/");
  await page.getByLabel("Primary navigation").getByRole("button", { name: "Network" }).click();
  await expect(page.getByText("Inventory unavailable")).toBeVisible();
  const beforeRetry = calls;
  allowSuccess = true;
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.getByText("192.0.2.9/24")).toBeVisible();
  expect(calls).toBeGreaterThan(beforeRetry);

  const beforeRefresh = calls;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByText("192.0.2.9/24")).toBeVisible();
  expect(calls).toBeGreaterThan(beforeRefresh);
});

test("Persian mode is RTL and exposes the live relay workflow", async ({ page }) => {
  await page.addInitScript(() => window.localStorage.setItem("egress.language", "fa"));
  await mockLiveDashboard(page);

  await page.goto("/");
  await expect(page.locator("html")).toHaveAttribute("lang", "fa");
  await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
  await expect(page.getByRole("heading", { name: "وضعیت فعلی سرور" })).toBeVisible();
  await expect(page.getByText("همه اطلاعات این صفحه مستقیماً از سرور خوانده می‌شود و هیچ ترافیک، مسیر، رله یا وضعیت سرویس ساختگی نمایش داده نمی‌شود.")).toBeVisible();
  const navigation = page.getByLabel("منوی اصلی");
  await expect(navigation.getByRole("button", { name: "داشبورد" })).toBeVisible();
  await expect(navigation.getByRole("button", { name: "رله‌ها" })).toBeVisible();

  await navigation.getByRole("button", { name: "رله‌ها" }).click();
  await expect(page.getByRole("heading", { name: "رله‌های ورودی" })).toBeVisible();
  await page.locator(".topbar").getByRole("button", { name: "رله جدید" }).click();
  const relayDialog = page.getByRole("dialog");
  await expect(relayDialog.getByRole("heading", { name: "ساخت رله ورودی" })).toBeVisible();
  await expect(relayDialog.getByLabel("پورت شنود")).toBeVisible();
  await relayDialog.getByRole("button", { name: "انصراف" }).click();

  await navigation.getByRole("button", { name: "خروجی‌ها" }).click();
  await expect(page.getByRole("heading", { name: "اتصال‌های خروجی" })).toBeVisible();
  await expect(page.getByText("رله Xray")).toBeVisible();
});

test("Xray empty state explains bundled runtime and scan button really rescans", async ({ page }) => {
  let discoveryCalls = 0;
  await page.route("**/api/v1/xray/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/discovery")) {
      discoveryCalls += 1;
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ installations: [], warnings: [] }) });
      return;
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [], next_cursor: "" }) });
  });

  await page.goto("/");
  await page.getByLabel("Primary navigation").getByRole("button", { name: "Xray" }).click();
  await expect(page.getByText("No Xray service detected")).toBeVisible();
  const scanButtons = page.getByRole("button", { name: "Scan again" });
  await expect(scanButtons).toHaveCount(2);
  await scanButtons.first().click();
  await expect.poll(() => discoveryCalls).toBeGreaterThan(1);
});
