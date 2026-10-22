export interface ServerConfig {
  port: number;
  databaseUrl: string;
  airwallexClientId: string;
  airwallexApiKey: string;
  airwallexWebhookSecret: string;
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): ServerConfig {
  const required = (name: string): string => {
    const value = env[name]?.trim();
    if (!value) throw new Error(`Missing required environment variable: ${name}`);
    return value;
  };
  const port = Number(env.PORT?.trim() || "3000");
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error("PORT must be an integer between 1 and 65535");
  }
  return {
    port,
    databaseUrl: required("DATABASE_URL"),
    airwallexClientId: required("AIRWALLEX_CLIENT_ID"),
    airwallexApiKey: required("AIRWALLEX_API_KEY"),
    airwallexWebhookSecret: required("AIRWALLEX_WEBHOOK_SECRET"),
  };
}
