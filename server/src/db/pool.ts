import type { ServerConfig } from "../config.js";

export interface QueryResult<Row = Record<string, unknown>> {
  rows: Row[];
  rowCount: number | null;
}

export interface DatabaseClient {
  query<Row = Record<string, unknown>>(
    text: string,
    values?: readonly unknown[],
  ): Promise<QueryResult<Row>>;
  release(): void;
}

export interface DatabasePool {
  query<Row = Record<string, unknown>>(
    text: string,
    values?: readonly unknown[],
  ): Promise<QueryResult<Row>>;
  connect(): Promise<DatabaseClient>;
  end(): Promise<void>;
}

export async function createDatabasePool(config: Pick<ServerConfig, "databaseUrl">): Promise<DatabasePool> {
  const { Pool } = await import("pg");
  return new Pool({ connectionString: config.databaseUrl }) as DatabasePool;
}
