import { expect, test } from "@playwright/test";

test("shell search, dashboard shortcuts, and unfinished pages have honest actions", async ({ page }) => {
  await page.goto("/");

  await page.getByRole("button", { name: "View all" }).click();
  await expect(page.getByRole("heading", { name: "Egress routes" })).toBeVisible();

  await page.getByRole("search").getByPlaceholder("Search").fill("Xray");
  await page.getByRole("search").getByPlaceholder("Search").press("Enter");
  await expect(page.getByRole("heading", { name: "Native Xray routing" })).toBeVisible();

  await page.getByLabel("Primary navigation").getByRole("button", { name: "Firewall" }).click();
  await expect(page.getByRole("heading", { name: "This page is not available yet" })).toBeVisible();
  await expect(page.locator(".topbar").getByRole("button", { name: "New route" })).toHaveCount(0);
});

test("network inventory retry button performs a new request", async ({ page }) => {
  let calls = 0;
  await page.route("**/api/v1/network/inventory", async (route) => {
    calls += 1;
    if (calls === 1) {
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
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.getByText("192.0.2.9/24")).toBeVisible();
  expect(calls).toBe(2);
});

test("Persian mode is RTL and uses plain-language labels", async ({ page }) => {
  await page.addInitScript(() => window.localStorage.setItem("egress.language", "fa"));
  await page.route("**/api/v1/outbounds**", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [], next_cursor: "" }) });
  });

  await page.goto("/");
  await expect(page.locator("html")).toHaveAttribute("lang", "fa");
  await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
  await expect(page.getByRole("heading", { name: "ارتباط‌ها عادی هستند." })).toBeVisible();
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
