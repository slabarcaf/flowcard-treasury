// Local operator helper. Secrets stay in process environment and are never printed.
import { createHmac, randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";

const [mode, argument] = process.argv.slice(2);
let base;
try {
  base = new URL(process.env.SHORTFALL_LOCAL_URL ?? "http://127.0.0.1:8082");
} catch {
  // Native URL errors can embed the supplied value. Never surface that value.
  throw new Error("local loopback URL required");
}
if (!["http:", "https:"].includes(base.protocol) ||
    !["localhost", "127.0.0.1", "[::1]"].includes(base.hostname) ||
    base.username || base.password || base.search || base.hash || base.pathname !== "/") {
  throw new Error("local loopback URL required");
}
const secret = (name) => {
  if (!process.env[name]) throw new Error(`${name} required`);
  return process.env[name];
};
const headers = {};
let path;
let body;
if (mode === "webhook") {
  path = "/webhooks/airwallex/shortfalls";
  body = await readFile(argument ?? new URL("../internal/provider/testdata/decline-envelope.json", import.meta.url));
  const timestamp = String(Date.now());
  headers["x-timestamp"] = timestamp;
  headers["x-signature"] = createHmac("sha256", secret("SHORTFALL_WEBHOOK_SECRET"))
    .update(timestamp).update(body).digest("hex");
  headers["content-type"] = "application/json";
} else if (mode === "list" || mode === "get") {
  if (mode === "get" && !/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(argument ?? "")) {
    throw new Error("get requires a detection UUID");
  }
  const now = Math.floor(Date.now()/1000);
  const encode = (value) => Buffer.from(JSON.stringify(value)).toString("base64url");
  const input = `${encode({ alg: "HS256", typ: "JWT" })}.${encode({
    iss: "flowcard-node-proxy", aud: "flowcard-shortfall-service", sub: "shortfall-reader",
    iat: now, exp: now+30, jti: randomUUID(),
  })}`;
  headers.authorization = `Bearer ${input}.${createHmac("sha256", secret("SHORTFALL_SERVICE_SECRET")).update(input).digest("base64url")}`;
  path = mode === "get" ? `/api/shortfalls/${argument}` : `/api/shortfalls${argument ? `?${argument.replace(/^\?/, "")}` : ""}`;
} else {
  throw new Error("usage: request.mjs webhook [file] | list [query] | get UUID");
}
try {
  const response = await fetch(new URL(path, base), { method: body ? "POST" : "GET", headers, body,
    redirect: "error", signal: AbortSignal.timeout(10_000) });
  const result = await response.json();
  console.log(JSON.stringify({ status: response.status, result }, null, 2));
  if (!response.ok) process.exitCode = 1;
} catch {
  // Never print fetch errors, URLs, headers or raw response bodies.
  console.error("local request unavailable");
  process.exitCode = 1;
}
