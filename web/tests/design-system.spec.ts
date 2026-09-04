import { expect, test } from "@playwright/test";

test("desktop shell exposes navigation, data, dialog, and keyboard focus", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Traffic is flowing normally." })).toBeVisible();
  if (process.env.VISUAL_QA) {
    await page.screenshot({ path: "test-results/dashboard.png", fullPage: true });
  }
  await expect(page.getByLabel("Primary navigation").getByRole("button")).toHaveCount(9);
  await expect(page.getByRole("table")).toBeVisible();

  await page.keyboard.press("Tab");
  await expect(page.getByRole("link", { name: "Skip to content" })).toBeFocused();

  await page.getByRole("button", { name: "New route" }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(page.getByLabel("Route name")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toBeHidden();
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
  await drawer.getByRole("button", { name: "Firewall" }).click();
  await expect(page.getByRole("heading", { name: "Firewall" })).toBeVisible();
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
