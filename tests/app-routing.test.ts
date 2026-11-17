import { describe, expect, it } from "vitest";
import { screenFromHash } from "../src/app-routing.js";

describe("App route fixtures", () => {
  it.each([
    ["", "landing"],
    ["#bridge-capital", "dashboard"],
    ["#portal", "portal"],
    ["#kit5", "landing"],
    ["#unknown", "landing"],
  ] as const)("maps %s to %s", (hash, expectedScreen) => {
    expect(screenFromHash(hash)).toBe(expectedScreen);
  });
});
