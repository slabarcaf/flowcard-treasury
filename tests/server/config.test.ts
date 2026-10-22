import { describe, expect, it, vi } from "vitest";
import { loadConfig } from "../../server/src/config.js";
import { handleRequest } from "../../server/src/http/server.js";

const validEnv = {
  DATABASE_URL: "postgres://localhost/flowcard",
  AIRWALLEX_CLIENT_ID: "local-client",
  AIRWALLEX_API_KEY: "local-secret",
  AIRWALLEX_WEBHOOK_SECRET: "webhook-secret",
};

describe("server configuration", () => {
  it("reports missing required environment variables", () => {
    expect(() => loadConfig({})).toThrow("DATABASE_URL");
    const { AIRWALLEX_WEBHOOK_SECRET: _secret, ...missingWebhookSecret } = validEnv;
    expect(() => loadConfig(missingWebhookSecret)).toThrow("AIRWALLEX_WEBHOOK_SECRET");
  });

  it("loads required values from the environment", () => {
    expect(loadConfig(validEnv)).toEqual({
      port: 3000,
      databaseUrl: validEnv.DATABASE_URL,
      airwallexClientId: validEnv.AIRWALLEX_CLIENT_ID,
      airwallexApiKey: validEnv.AIRWALLEX_API_KEY,
      airwallexWebhookSecret: validEnv.AIRWALLEX_WEBHOOK_SECRET,
    });
  });

  it("loads the optional Salesforce snapshot read token", () => {
    expect(loadConfig({ ...validEnv, SALESFORCE_SNAPSHOT_READ_TOKEN: "  read-secret  " }).salesforceSnapshotReadToken)
      .toBe("read-secret");
  });
});

describe("health route", () => {
  it("returns an ok response", () => {
    const response = { writeHead: vi.fn(), end: vi.fn() };
    handleRequest({ method: "GET", url: "/health" } as never, response as never);
    expect(response.writeHead).toHaveBeenCalledWith(200, { "content-type": "application/json" });
    expect(response.end).toHaveBeenCalledWith(JSON.stringify({ status: "ok" }));
  });
});
