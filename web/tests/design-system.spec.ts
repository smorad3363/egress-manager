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
