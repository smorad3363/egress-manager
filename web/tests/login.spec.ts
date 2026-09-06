import { expect, test } from "@playwright/test";

test("administrator login stores CSRF token and enters console", async ({ page }) => {
  await page.route("**/api/v1/auth/login", async (route) => {
    expect(route.request().method()).toBe("POST");
    expect(route.request().postDataJSON()).toEqual({ username: "operator", password: "correct horse battery staple" });
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ csrf_token: "csrf-test-token", expires_at: "2026-09-07T08:00:00Z" }),
    });
  });
  await page.goto("/login");
  await expect(page.getByRole("heading", { name: "Sign in to this gateway" })).toBeVisible();
  await expect(page.getByLabel("Username")).toBeFocused();
  await page.getByLabel("Username").fill("operator");
  await page.getByLabel("Password").fill("correct horse battery staple");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("/");
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem("egress.csrf"))).toBe("csrf-test-token");
});
