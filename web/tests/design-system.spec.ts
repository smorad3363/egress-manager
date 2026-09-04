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
