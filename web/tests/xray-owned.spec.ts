import { expect, test } from "@playwright/test";

async function login(page: import("@playwright/test").Page) {
  await page.goto("/login");
  await page.getByLabel("Username").fill("operator");
  await page.getByLabel("Password").fill("operator-password");
  await page.getByRole("button", { name: /sign in|login/i }).click();
  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
}

test("Xray page is project-owned and has no foreign-panel discovery controls", async ({ page }) => {
  await login(page);
  await page.getByRole("button", { name: "Xray", exact: true }).click();

  await expect(page.getByRole("heading", { name: "Managed Xray runtime" })).toBeVisible();
  await expect(page.getByText("Egress Manager Xray", { exact: true })).toBeVisible();
  await expect(page.getByText("/usr/local/lib/egress-manager/bin/xray", { exact: true })).toBeVisible();
  await expect(page.getByText("egress-manager-xray-relay.service", { exact: true })).toBeVisible();
  await expect(page.getByText("/var/lib/egress-manager/private/xray-relay.json", { exact: true })).toBeVisible();

  await expect(page.getByText(/Marzban/i)).toHaveCount(0);
  await expect(page.getByText(/3x-ui/i)).toHaveCount(0);
  await expect(page.getByRole("button", { name: /scan again|new binding|apply & restart xray/i })).toHaveCount(0);
});
