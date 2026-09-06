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

async function mockLiveDashboard(page: Page) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/v1/control/health") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ status: "ok", version: "v0.1.0-alpha.8", checks: { database: "ok", egressd: "ok" } }) });
      return;
    }
    if (url.pathname === "/api/v1/routes") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [{ route: liveRoute, revision: 4 }], next_cursor: "" }) });
      return;
    }
    if (url.pathname === "/api/v1/outbounds") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [{ outbound: liveOutbound, revision: 2, created_at: "2026-09-06T12:00:00Z", updated_at: "2026-09-06T12:00:00Z" }], next_cursor: "" }) });
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
  await expect(page.getByText("EU applications")).toHaveCount(0);
  await expect(page.getByText("Frankfurt · WG0")).toHaveCount(0);
  await expect(page.getByText("4.6 TB")).toHaveCount(0);
  await expect(page.getByText("99.98%")).toHaveCount(0);

  const navigation = page.getByLabel("Primary navigation");
  await expect(navigation.getByRole("button")).toHaveCount(7);
  await expect(navigation.getByRole("button", { name: "Firewall" })).toHaveCount(0);
  await expect(navigation.getByRole("button", { name: "Logs" })).toHaveCount(0);
  await expect(navigation.getByRole("button", { name: "Settings" })).toHaveCount(0);

  await page.getByRole("button", { name: /View all/ }).click();
  await expect(page.getByRole("heading", { name: "Interface & subnet routes" })).toBeVisible();

  await navigation.getByRole("button", { name: "Dashboard" }).click();
  await page.locator(".topbar").getByRole("button", { name: "New route" }).click();
  await expect(page.getByRole("dialog").getByRole("heading", { name: "Create egress route" })).toBeVisible();
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();

  await page.getByRole("search").getByPlaceholder("Search").fill("Xray");
  await page.getByRole("search").getByPlaceholder("Search").press("Enter");
  await expect(page.getByRole("heading", { name: "Native Xray routing" })).toBeVisible();
  await expect(page.locator(".topbar").getByRole("button", { name: "New binding" })).toHaveCount(0);
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

test("Persian mode is RTL and dashboard copy describes live server data", async ({ page }) => {
  await page.addInitScript(() => window.localStorage.setItem("egress.language", "fa"));
  await mockLiveDashboard(page);

  await page.goto("/");
  await expect(page.locator("html")).toHaveAttribute("lang", "fa");
  await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
  await expect(page.getByRole("heading", { name: "وضعیت فعلی سرور" })).toBeVisible();
  await expect(page.getByText("همه اطلاعات این صفحه مستقیماً از سرور خوانده می‌شود و هیچ مقدار نمونه یا ساختگی نمایش داده نمی‌شود.")).toBeVisible();
  await expect(page.getByLabel("منوی اصلی").getByRole("button", { name: "داشبورد" })).toBeVisible();

  await page.getByLabel("منوی اصلی").getByRole("button", { name: "خروجی‌ها" }).click();
  await expect(page.getByRole("heading", { name: "اتصال‌های خروجی" })).toBeVisible();
  await page.locator(".topbar").getByRole("button", { name: "افزودن اتصال" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "افزودن اتصال" })).toBeVisible();
  await expect(dialog.getByText("لینک اتصال را اینجا بچسبانید. در صورت نیاز می‌توانید JSON مربوط به sing-box را هم وارد کنید.")).toBeVisible();
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
