import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";

async function mockDashboard(page: Page) {
  await page.route("**/api/v1/control/health", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ status: "ok", version: "v0.1.0-alpha.8", checks: { database: "ok" } }) }));
  await page.route("**/api/v1/routes?limit=100", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [{ route: { id: "office", name: "Office route", source: { kind: "subnet", subnet: "10.10.0.0/16" }, outbound_id: "primary", failure_policy: "block", dns_policy: "follow_outbound", dns_servers: ["1.1.1.1"], ipv4_policy: "follow_outbound", ipv6_policy: "block", kill_switch: true, enabled: true }, revision: 1 }] }) }));
  await page.route("**/api/v1/routes?limit=100", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [{ route: { id: "office", name: "Office route", source: { kind: "subnet", subnet: "10.10.0.0/16" }, outbound_id: "primary", failure_policy: "block", dns_policy: "follow_outbound", dns_servers: ["1.1.1.1"], ipv4_policy: "follow_outbound", ipv6_policy: "block", kill_switch: true, enabled: true }, revision: 1 }] }) }));
  await page.route("**/api/v1/outbounds?limit=100", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [{ outbound: { id: "primary", name: "Primary outbound", enabled: true, health: { status: "healthy" } }, revision: 1 }] }) }));
  await page.route("**/api/v1/outbounds?limit=128", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [{ outbound: { id: "primary", name: "Primary outbound", type: "socks5", enabled: true, health: { status: "healthy" } }, revision: 1 }] }) }));
  await page.route("**/api/v1/relays?limit=100", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [] }) }));
  await page.route("**/api/v1/port-forwards?limit=100", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [] }) }));
  await page.route("**/api/v1/port-forwards/counters?family=ipv4", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [] }) }));
  await page.route("**/api/v1/port-forwards/counters?family=ipv6", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [] }) }));
  await page.route("**/api/v1/haproxy/stats", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ info: { version: "3.0", pid: 10 }, stats: [] }) }));
}

test("desktop shell exposes live data, finished navigation, real dialog, and keyboard focus", async ({ page }) => {
  await mockDashboard(page);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Current server overview" })).toBeVisible();
  if (process.env.VISUAL_QA) {
    await page.screenshot({ path: "test-results/dashboard.png", fullPage: true });
  }
  await expect(page.getByLabel("Primary navigation").getByRole("button")).toHaveCount(8);
  await expect(page.getByRole("table")).toBeVisible();

  await page.keyboard.press("Tab");
  await expect(page.getByRole("link", { name: "Skip to content" })).toBeFocused();

  await page.locator(".topbar").getByRole("button", { name: "New route" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "Create egress route" })).toBeVisible();
  await expect(dialog.getByLabel("Route ID")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
});

test("mobile navigation remains operable", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  if (process.env.VISUAL_QA) {
    await page.screenshot({ path: "test-results/dashboard-mobile.png", fullPage: true });
  }
  await expect(page.getByRole("button", { name: "Open navigation" })).toBeVisible();
  await page.getByRole("button", { name: "Open navigation" }).click();
  const drawer = page.locator(".mobile-drawer__panel");
  await expect(drawer.getByRole("button", { name: "Port Forward" })).toBeVisible();
  await expect(drawer.getByRole("button", { name: "Firewall" })).toHaveCount(0);
  await drawer.getByRole("button", { name: "Port Forward" }).click();
  await expect(page.getByRole("heading", { name: "Port forwarding" })).toBeVisible();
});

test("design-system fixtures and reduced motion are available", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/design-system");
  await expect(page.getByRole("heading", { name: "Interface system" })).toBeVisible();
  await expect(page.getByText("No routes yet")).toBeVisible();
  await expect(page.getByText("Health check failed")).toBeVisible();
  await page.getByRole("button", { name: "Open dialog" }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  const duration = await page.locator(".skeleton").first().evaluate((element) => getComputedStyle(element).animationDuration);
  expect(Number.parseFloat(duration)).toBeLessThan(0.001);
});

test("authenticated network inventory displays typed host state", async ({ page }) => {
  await page.route("**/api/v1/network/inventory", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        generated_at: "2026-09-04T08:00:00Z",
        interfaces: [{ name: "eth0", state: "up", mtu: 1500, addresses: [{ family: "inet", cidr: "192.0.2.9/24", scope: "global" }] }],
        routes: [{ destination: "default", gateway: "192.0.2.1", interface: "eth0", default: true }],
        listeners: [{ protocol: "tcp", address: "0.0.0.0", port: 443, process: "haproxy", pid: 42 }],
        dns: { source: "/etc/resolv.conf", servers: ["1.1.1.1"], search_domains: ["internal.example"] },
        capabilities: [{ name: "nftables", available: true, running: false, version: "nftables v1.0.9" }],
        warnings: [],
      }),
    });
  });
  await page.goto("/");
  await page.getByLabel("Primary navigation").getByRole("button", { name: "Network" }).click();
  await expect(page.getByRole("heading", { name: "Host network inventory" })).toBeVisible();
  await expect(page.getByText("192.0.2.9/24")).toBeVisible();
  await expect(page.getByRole("cell", { name: "haproxy PID 42" })).toBeVisible();
  await expect(page.getByText("nftables v1.0.9")).toBeVisible();
});

test("port-forward console previews and applies owned NAT plan", async ({ page }) => {
  await page.addInitScript(() => window.sessionStorage.setItem("egress.csrf", "test-csrf-token"));
  await page.route("**/api/v1/port-forwards**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() !== "GET") expect(request.headers()["x-csrf-token"]).toBe("test-csrf-token");
    if (url.pathname.endsWith("/counters")) {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ family: "ipv4", items: [{ forward_id: "web_tls", accepted_packets: 1284, accepted_bytes: 987654, dropped_packets: 12, dropped_bytes: 768 }] }) });
      return;
    }
    if (url.pathname.endsWith("/plan")) {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ engine: "nftables", family: "ipv4", owned_table: "ip egm_nat4", enabled_rules: 1, actions: [{ kind: "replace", resource: "ip egm_nat4", summary: "Replace only the project-owned NAT table." }, { kind: "forward", resource: "web_tls", summary: "Install owned DNAT, forwarding, and masquerade rules." }], candidate: "table ip egm_nat4 {\n  comment \"Egress Manager owned NAT table\"\n}" }) });
      return;
    }
    if (url.pathname.endsWith("/apply")) {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ transaction_id: "nat_test", state: "COMMITTED" }) });
      return;
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [{ forward: { id: "web_tls", name: "Public HTTPS", protocols: ["tcp"], listen_address: "203.0.113.10", listen_ports: [{ from: 8443, to: 8443 }], remote_address: "10.10.0.5", remote_port_start: 443, source_cidrs: ["198.51.100.0/24"], enabled: true }, revision: 1, created_at: "2026-09-04T08:00:00Z", updated_at: "2026-09-04T08:00:00Z" }] }) });
  });

  await page.goto("/");
  await page.getByLabel("Primary navigation").getByRole("button", { name: "Port Forward" }).click();
  await expect(page.getByRole("heading", { name: "Port forwarding" })).toBeVisible();
  await expect(page.getByText("Public HTTPS")).toBeVisible();
  await expect(page.getByText("1,296")).toBeVisible();
  if (process.env.VISUAL_QA) await page.screenshot({ path: "test-results/port-forwards.png", fullPage: true });

  await page.getByRole("button", { name: "New forward" }).click();
  await expect(page.getByRole("dialog").getByRole("heading", { name: "Create port forward" })).toBeVisible();
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();

  await page.getByRole("button", { name: "Review & apply" }).click();
  const plan = page.getByRole("dialog");
  await expect(plan.getByRole("heading", { name: "Review NAT plan" })).toBeVisible();
  await plan.getByText("Generated native changes").click();
  await expect(plan.getByText("Egress Manager owned NAT table")).toBeVisible();
  await plan.getByRole("button", { name: "Apply atomically" }).click();
  await expect(plan).toBeHidden();
});

test("HAProxy console shows health outcomes and graceful apply review", async ({ page }) => {
  await page.addInitScript(() => window.sessionStorage.setItem("egress.csrf", "test-csrf-token"));
  await page.route("**/api/v1/haproxy/**", async (route) => {
    const request = route.request(); const url = new URL(request.url());
    if (request.method() !== "GET") expect(request.headers()["x-csrf-token"]).toBe("test-csrf-token");
    if (url.pathname.endsWith("/frontends")) { await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [{ frontend: { id: "public_api", name: "Public API", bind: "192.0.2.10", port: 443, backend_ids: ["api_primary", "api_backup"], algorithm: "leastconn", enabled: true }, revision: 1 }] }) }); return; }
    if (url.pathname.endsWith("/backends")) { await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [{ backend: { id: "api_primary", name: "API Primary", server: { host: "10.20.0.10", port: 8080 }, weight: 100, backup: false, health_check: true, health: { status: "unknown" }, enabled: true }, revision: 1 }, { backend: { id: "api_backup", name: "API Backup", server: { host: "10.20.0.11", port: 8080 }, weight: 50, backup: true, health_check: true, health: { status: "unknown" }, enabled: true }, revision: 1 }] }) }); return; }
    if (url.pathname.endsWith("/stats")) { await route.fulfill({ contentType: "application/json", body: JSON.stringify({ info: { name: "HAProxy", version: "3.0.5", pid: 42, uptime_seconds: 3600, current_connections: 12, total_connections: 4812 }, stats: [{ frontend_id: "public_api", backend_id: "api_primary", kind: "server", status: "healthy", current_sessions: 8, total_sessions: 3200, bytes_in: 1024000, bytes_out: 4096000, check_failures: 0, downtime_seconds: 0 }, { frontend_id: "public_api", backend_id: "api_backup", kind: "server", status: "healthy", current_sessions: 0, total_sessions: 20, bytes_in: 1000, bytes_out: 2000, check_failures: 1, downtime_seconds: 4 }] }) }); return; }
    if (url.pathname.endsWith("/plan")) { await route.fulfill({ contentType: "application/json", body: JSON.stringify({ engine: "haproxy", state_hash: "test", enabled_frontends: 1, enabled_backends: 2, actions: [{ kind: "replace", resource: "owned HAProxy configuration", summary: "Atomically replace the project-owned HAProxy configuration." }], candidate: "# Egress Manager owned HAProxy configuration\nfrontend egm_fe_public_api\n  bind 192.0.2.10:443\n" }) }); return; }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ transaction_id: "haproxy_test", state: "COMMITTED", runtime: { info: { pid: 43 }, stats: [] } }) });
  });
  await page.goto("/");
  await page.getByLabel("Primary navigation").getByRole("button", { name: "HAProxy" }).click();
  await expect(page.getByRole("heading", { name: "HAProxy", level: 2 })).toBeVisible();
  await expect(page.getByText("Public API")).toBeVisible();
  await expect(page.getByText("API Primary", { exact: true })).toBeVisible();
  await expect(page.getByText("2 / 2")).toBeVisible();
  if (process.env.VISUAL_QA) await page.screenshot({ path: "test-results/haproxy.png", fullPage: true });
  await page.getByRole("button", { name: "New frontend" }).click();
  await expect(page.getByRole("dialog").getByRole("heading", { name: "Create frontend" })).toBeVisible();
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();
  await page.getByRole("button", { name: "Review & apply" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "Review HAProxy plan" })).toBeVisible();
  await dialog.getByText("Advanced read-only configuration").click();
  await expect(dialog.getByText("frontend egm_fe_public_api", { exact: false })).toBeVisible();
  await dialog.getByRole("button", { name: "Apply & reload" }).click();
  await expect(dialog).toBeHidden();
});

test("outbound console tests, stores, manages, and applies without exposing credentials", async ({ page }) => {
  const secret = "browser-fixture-password";
  const stored = { outbound: { id: "ams_socks", name: "Amsterdam SOCKS", adapter: "sing-box", type: "socks5", server: { host: "192.0.2.30", port: 1080 }, capabilities: { tcp: true, udp: true }, health: { status: "healthy", checked_at: "2026-09-04T08:00:00Z", configuration_valid: "passed", transport_reachable: "passed", internet_reachable: "passed", external_ip: "203.0.113.40", tcp: "passed", udp: "untestable", latency: 24000000 }, enabled: true, secret_metadata: ["username", "password"] }, revision: 3, created_at: "2026-09-04T08:00:00Z", updated_at: "2026-09-04T08:05:00Z" };
  const calls: string[] = [];
  await page.addInitScript(() => window.sessionStorage.setItem("egress.csrf", "test-csrf-token"));
  await page.route("**/api/v1/outbounds**", async (route) => {
    const request = route.request(); const url = new URL(request.url()); calls.push(`${request.method()} ${url.pathname}`);
    if (request.method() !== "GET") expect(request.headers()["x-csrf-token"]).toBe("test-csrf-token");
    if (request.method() === "GET") { await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [stored], next_cursor: "" }) }); return; }
    if (url.pathname.endsWith("/test")) { expect((await request.postDataJSON()).input).toContain(secret); await route.fulfill({ contentType: "application/json", body: JSON.stringify({ results: [{ outbound: stored.outbound, health: stored.outbound.health }] }) }); return; }
    if (url.pathname.endsWith("/import")) { expect((await request.postDataJSON()).input).toContain(secret); await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ outbounds: [stored.outbound] }) }); return; }
    if (url.pathname.endsWith("/plan")) { await route.fulfill({ contentType: "application/json", body: JSON.stringify({ engine: "sing-box", state_hash: "state-hash", candidate_hash: "candidate-hash", enabled_outbounds: 1, actions: [{ kind: "replace", resource: "owned sing-box configuration", summary: "Atomically replace the project-owned sing-box configuration." }, { kind: "outbound", resource: "ams_socks", summary: "Configure an enabled sing-box outbound." }] }) }); return; }
    if (url.pathname.endsWith("/apply")) { expect(await request.postDataJSON()).toEqual({ expected_state_hash: "state-hash", expected_candidate_hash: "candidate-hash" }); await route.fulfill({ contentType: "application/json", body: JSON.stringify({ transaction_id: "singbox_test", state: "COMMITTED", candidate_hash: "candidate-hash" }) }); return; }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(stored) });
  });
  await page.goto("/");
  await page.getByLabel("Primary navigation").getByRole("button", { name: "Outbounds" }).click();
  await expect(page.getByRole("heading", { name: "Outbound connections" })).toBeVisible();
  await expect(page.getByText("Amsterdam SOCKS")).toBeVisible();
  await expect(page.getByText("203.0.113.40")).toBeVisible();
  await expect(page.getByText("TCP + UDP")).toBeVisible();
  if (process.env.VISUAL_QA) await page.screenshot({ path: "test-results/outbounds.png", fullPage: true });

  await page.getByRole("button", { name: "Import outbound" }).first().click();
  const importDialog = page.getByRole("dialog");
  await importDialog.getByLabel("URI or sing-box JSON").fill(`socks5://operator:${secret}@192.0.2.31:1080#Imported`);
  await importDialog.getByRole("button", { name: "Test connection" }).click();
  await expect(importDialog.getByLabel("Health healthy")).toBeVisible();
  await importDialog.getByRole("button", { name: "Save outbound" }).click();
  await expect(importDialog).toBeHidden();
  await expect(page.getByText(secret)).toHaveCount(0);

  await page.getByRole("button", { name: "Disable" }).click();
  expect(calls).toContain("PUT /api/v1/outbounds");
  await page.getByRole("button", { name: "Review sing-box apply" }).click();
  const plan = page.getByRole("dialog");
  await expect(plan.getByRole("heading", { name: "Review sing-box plan" })).toBeVisible();
  await expect(plan.getByText("candidate-hash")).toHaveCount(0);
  await plan.getByRole("button", { name: "Apply sing-box" }).click();
  await expect(plan).toBeHidden();
  expect(calls).toContain("POST /api/v1/outbounds/apply");
});

test("route console manages explicit policy and applies only reviewed hashes", async ({ page }) => {
  const storedRoute = { route: { id: "vpn_clients", name: "VPN clients", source: { kind: "interface", interface: "tun0" }, outbound_id: "ams_socks", failure_policy: "block", dns_policy: "follow_outbound", dns_servers: ["1.1.1.1"], ipv4_policy: "follow_outbound", ipv6_policy: "block", kill_switch: true, mtu: 1400, tcp_mss: 1360, enabled: true }, revision: 2 };
  const outbound = { outbound: { id: "ams_socks", name: "Amsterdam SOCKS", adapter: "sing-box", type: "socks5", server: { host: "192.0.2.30", port: 1080 }, capabilities: { tcp: true, udp: true }, health: { status: "healthy" }, enabled: true }, revision: 3 };
  const calls: string[] = [];
  await page.addInitScript(() => window.sessionStorage.setItem("egress.csrf", "test-csrf-token"));
  await page.route("**/api/v1/outbounds?limit=128", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [outbound] }) }));
  await page.route("**/api/v1/routes**", async (route) => {
    const request = route.request(); const url = new URL(request.url()); calls.push(`${request.method()} ${url.pathname}`);
    if (request.method() !== "GET") expect(request.headers()["x-csrf-token"]).toBe("test-csrf-token");
    if (request.method() === "GET") { await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [storedRoute], next_cursor: "" }) }); return; }
    if (url.pathname.endsWith("/plan")) { await route.fulfill({ contentType: "application/json", body: JSON.stringify({ engine: "coordinated-egress-routing", sing_box_state_hash: "sb-state", routing_state_hash: "route-state", interface_state_hash: "interface-state", sing_box_candidate_hash: "sb-candidate", routing_candidate_hash: "route-candidate", native_candidate_hash: "native-candidate", combined_candidate_hash: "combined-candidate", enabled_outbounds: 1, enabled_routes: 1, sing_box_actions: [{ kind: "route", resource: "vpn_clients", summary: "Configure a dedicated TUN." }], routing_actions: [{ kind: "kill_switch", resource: "vpn_clients", summary: "Block unintended fallback." }], native_actions: [{ kind: "policy_route", resource: "vpn_clients", summary: "Install owned IP policy." }] }) }); return; }
    if (url.pathname.endsWith("/apply")) { expect(await request.postDataJSON()).toEqual({ expected_sing_box_state_hash: "sb-state", expected_routing_state_hash: "route-state", expected_interface_state_hash: "interface-state", expected_sing_box_candidate_hash: "sb-candidate", expected_routing_candidate_hash: "route-candidate", expected_native_candidate_hash: "native-candidate", expected_combined_candidate_hash: "combined-candidate" }); await route.fulfill({ contentType: "application/json", body: JSON.stringify({ transaction_id: "route_test", state: "COMMITTED", combined_candidate_hash: "combined-candidate" }) }); return; }
    await route.fulfill({ status: request.method() === "POST" ? 201 : 200, contentType: "application/json", body: JSON.stringify(storedRoute) });
  });
  await page.goto("/");
  await page.getByLabel("Primary navigation").getByRole("button", { name: "Routes" }).click();
  await expect(page.getByRole("heading", { name: "Interface & subnet routes" })).toBeVisible();
  await expect(page.getByText("VPN clients")).toBeVisible();
  await expect(page.getByText("block · kill switch")).toBeVisible();
  if (process.env.VISUAL_QA) await page.screenshot({ path: "test-results/routes.png", fullPage: true });
  await page.locator(".topbar").getByRole("button", { name: "New route" }).click();
  const editor = page.getByRole("dialog");
  await expect(editor.getByRole("heading", { name: "Create egress route" })).toBeVisible();
  await editor.getByLabel("Route ID").fill("branch_clients");
  await editor.getByLabel("Display name").fill("Branch clients");
  await editor.getByRole("textbox", { name: "Interface", exact: true }).fill("tun1");
  await editor.getByLabel("Primary outbound").selectOption("ams_socks");
  await editor.getByRole("button", { name: "Save route" }).click();
  await expect(editor).toBeHidden();
  expect(calls).toContain("POST /api/v1/routes");
  await page.getByRole("button", { name: "Review & apply" }).click();
  const review = page.getByRole("dialog");
  await expect(review.getByRole("heading", { name: "Review egress route plan" })).toBeVisible();
  await expect(review.getByText("Block unintended fallback.")).toBeVisible();
  await expect(review.getByText("combined-candidate")).toHaveCount(0);
  await review.getByRole("button", { name: "Apply atomically" }).click();
  await expect(review).toBeHidden();
  expect(calls).toContain("POST /api/v1/routes/apply");
});

test("Xray console is project-owned and ignores foreign panel discovery data", async ({ page }) => {
  const calls: string[] = [];
  const xrayOutbound = {
    outbound: {
      id: "xray_reality",
      name: "Reality XHTTP",
      adapter: "xray",
      type: "vless",
      server: { host: "198.51.100.30", port: 443 },
      capabilities: { tcp: true, udp: true },
      health: { status: "healthy" },
      enabled: true,
    },
    revision: 3,
  };
  const relay = {
    relay: {
      id: "ssh_gateway",
      name: "SSH gateway",
      listen_address: "0.0.0.0",
      listen_port: 6111,
      network: "tcp",
      destination: { host: "91.107.220.12", port: 6111 },
      outbound_id: "xray_reality",
      enabled: true,
    },
    revision: 2,
  };

  await page.route("**/api/v1/outbounds?limit=128", async (route) => {
    calls.push(`GET ${new URL(route.request().url()).pathname}`);
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [xrayOutbound] }) });
  });
  await page.route("**/api/v1/relays?limit=128", async (route) => {
    calls.push(`GET ${new URL(route.request().url()).pathname}`);
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [relay] }) });
  });
  await page.route("**/api/v1/xray/discovery", async (route) => {
    calls.push(`GET ${new URL(route.request().url()).pathname}`);
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({
      installations: [
        { kind: "standalone", config_path: "/etc/xray/config.json", service_name: "xray.service" },
        { kind: "marzban", config_path: "/var/lib/marzban/xray_config.json", service_name: "marzban.service" },
      ],
      warnings: [],
    }) });
  });

  await page.goto("/");
  await page.getByLabel("Primary navigation").getByRole("button", { name: "Xray" }).click();
  await expect(page.getByRole("heading", { name: "Native Xray routing" })).toBeVisible();
  await expect(page.getByText("Egress Manager Xray", { exact: true })).toBeVisible();
  await expect(page.getByText("/usr/local/lib/egress-manager/bin/xray", { exact: true })).toBeVisible();
  await expect(page.getByText("egress-manager-xray-relay.service", { exact: true })).toBeVisible();
  await expect(page.getByText("/var/lib/egress-manager/private/xray-relay.json", { exact: true })).toBeVisible();
  await expect(page.getByText("Reality XHTTP", { exact: true })).toBeVisible();
  await expect(page.getByText("SSH gateway", { exact: true })).toBeVisible();
  await expect(page.getByText("91.107.220.12:6111", { exact: true })).toBeVisible();
  await expect(page.getByText(/Marzban/i)).toHaveCount(0);
  await expect(page.getByText("/etc/xray/config.json", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "New binding" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Review & apply" })).toHaveCount(0);
  expect(calls.every((value) => value.startsWith("GET "))).toBe(true);
  if (process.env.VISUAL_QA) await page.screenshot({ path: "test-results/xray.png", fullPage: true });
});
