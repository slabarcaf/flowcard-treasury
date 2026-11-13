import { describe, expect, it } from "vitest";
import { legacyVersionFromStorage, screenFromHash } from "../src/app-routing.js";

describe("App route fixtures", () => {
  it.each([
    ["", "landing"],
    ["#bridge-capital", "dashboard"],
    ["#portal", "portal"],
    ["#kit5", "kit5"],
    ["#unknown", "landing"],
  ] as const)("maps %s to %s", (hash, expectedScreen) => {
    expect(screenFromHash(hash)).toBe(expectedScreen);
  });

  it("preserves the legacy GD/Kit 5 preference and defaults invalid values to Kit 5", () => {
    expect(legacyVersionFromStorage("gd")).toBe("gd");
    expect(legacyVersionFromStorage("kit5")).toBe("kit5");
    expect(legacyVersionFromStorage("unexpected")).toBe("kit5");
    expect(legacyVersionFromStorage(null)).toBe("kit5");
  });
});
