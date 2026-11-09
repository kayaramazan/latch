import { test, expect } from "@playwright/test";

// Requires a latchd with at least one relay configured (CI seeds it) and the
// embedded UI. Covers the SPEC success criterion: add a tunnel route.
test("add a tunnel route", async ({ page }) => {
  await page.goto("/routes/new");
  await page.getByTestId("target").fill("127.0.0.1:3000");
  await page.getByTestId("next").click();
  await page.getByTestId("mode-tunnel").click();
  await page.getByTestId("hostname").fill("app.relay.test");
  await expect(page.getByText("https://app.relay.test")).toBeVisible();
  await page.getByTestId("create").click();
  await expect(page).toHaveURL(/\/routes\//);
});
