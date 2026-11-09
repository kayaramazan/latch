import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "../test/render";
import { AddRoute } from "./AddRoute";

describe("AddRoute wizard", () => {
  beforeEach(() => {
    global.fetch = vi.fn(async (url: string | URL) => {
      const u = String(url);
      if (u.includes("/api/relays")) {
        return new Response(JSON.stringify([{ id: "rly_1", name: "vps", host: "relay.example.com", port: 7443, baseDomain: "relay.example.com" }]), { status: 200 });
      }
      return new Response("[]", { status: 200 });
    }) as unknown as typeof fetch;
  });

  it("walks the two steps and previews the URL", async () => {
    const user = userEvent.setup();
    renderApp(<AddRoute />);
    // Step 1.
    expect(screen.getByText("What are you publishing?")).toBeDefined();
    await user.type(screen.getByTestId("target"), "127.0.0.1:3000");
    await user.click(screen.getByTestId("next"));
    // Step 2: tunnel is the default; pick a hostname and see the preview.
    await waitFor(() => expect(screen.getByText("How should it be reached?")).toBeDefined());
    await user.type(screen.getByTestId("hostname"), "app.relay.example.com");
    await waitFor(() => expect(screen.getByText("https://app.relay.example.com")).toBeDefined());
    expect((screen.getByTestId("create") as HTMLButtonElement).disabled).toBe(false);
  });
});
