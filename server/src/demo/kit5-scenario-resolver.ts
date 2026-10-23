import type { DatabasePool } from "../db/pool.js";
import type { Kit5DepositTarget } from "../collections/kit5-repayment-deposit.js";
import type { Kit5CustomerFixture, NextCustomerInput } from "./next-customer.js";

interface DemoCustomerSpec {
  customerId: string;
  amountCents: number;
  merchant: string;
  merchantCategoryCode: string;
}

interface BindingRow {
  customer_id: string;
  roster_account_id: string;
  roster_active: boolean;
  card_id: string | null;
  binding_account_id: string | null;
  binding_active: boolean | null;
}

interface ResolvedBinding {
  customerId: string;
  accountId: string;
  cardId: string;
}

const SCENARIOS: Record<string, readonly DemoCustomerSpec[]> = {
  original: [
    { customerId: "acct_bloom001", amountCents: 900_000, merchant: "Supplier", merchantCategoryCode: "5411" },
    { customerId: "acct_atlas001", amountCents: 650_000, merchant: "Supplier", merchantCategoryCode: "5411" },
    { customerId: "acct_nova001", amountCents: 700_000, merchant: "Contractor", merchantCategoryCode: "5411" },
    { customerId: "acct_pixel001", amountCents: 250_000, merchant: "Advertising", merchantCategoryCode: "5411" },
  ],
  "kit5-target": [
    { customerId: "acct_bloom001", amountCents: 900_000, merchant: "Supplier", merchantCategoryCode: "5411" },
    { customerId: "acct_atlas001", amountCents: 650_000, merchant: "Supplier", merchantCategoryCode: "5411" },
    { customerId: "acct_nova001", amountCents: 700_000, merchant: "Contractor", merchantCategoryCode: "5411" },
    { customerId: "acct_pixel001", amountCents: 250_000, merchant: "Advertising", merchantCategoryCode: "5411" },
    { customerId: "acct_juniper001", amountCents: 700_000, merchant: "Contractor", merchantCategoryCode: "5411" },
  ],
  "first-come": [
    { customerId: "acct_cedar001", amountCents: 900_000, merchant: "Supplier", merchantCategoryCode: "5411" },
    { customerId: "acct_maple001", amountCents: 900_000, merchant: "Supplier", merchantCategoryCode: "5411" },
  ],
  "tight-escalates": [
    { customerId: "acct_orchid001", amountCents: 350_000, merchant: "Contractor", merchantCategoryCode: "5411" },
    { customerId: "acct_willow001", amountCents: 350_000, merchant: "Contractor", merchantCategoryCode: "5411" },
  ],
};

const NOVA_CUSTOMER_ID = "acct_nova001";
const KIT5_TARGET_SCENARIO_ID = "kit5-target";
const KIT5_BLOOM_CUSTOMER_ID = "acct_bloom001";
const KIT5_BLOOM_CHECKPOINT_REQUEST_ID = "txn_bloom_supplier_001";
const KIT5_BLOOM_CHECKPOINT_AMOUNT_CENTS = 900_000;
const SAFE_PROVIDER_ID = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$/;
const LIVE_RUN_ID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

/** The checkpoint action is fixed by the server and scoped to one v4 demo run. */
export function isKit5FixtureCheckpointInput(input: NextCustomerInput): boolean {
  const prefix = `${KIT5_TARGET_SCENARIO_ID}:`;
  if (!input.scenarioId.startsWith(prefix) || input.customerId !== KIT5_BLOOM_CUSTOMER_ID) return false;
  const runId = input.scenarioId.slice(prefix.length);
  return LIVE_RUN_ID_PATTERN.test(runId) &&
    input.actionId === `${input.scenarioId}:${KIT5_BLOOM_CHECKPOINT_REQUEST_ID}`;
}

function resolveScenarioSpecs(scenarioId: string): readonly DemoCustomerSpec[] | null {
  if (Object.prototype.hasOwnProperty.call(SCENARIOS, scenarioId)) return SCENARIOS[scenarioId] ?? null;
  const separator = scenarioId.indexOf(":");
  if (separator < 1 || scenarioId.indexOf(":", separator + 1) !== -1) return null;
  const presetId = scenarioId.slice(0, separator);
  const runId = scenarioId.slice(separator + 1);
  if (!LIVE_RUN_ID_PATTERN.test(runId) || !Object.prototype.hasOwnProperty.call(SCENARIOS, presetId)) return null;
  return SCENARIOS[presetId] ?? null;
}

/**
 * Resolves only known local scenarios and requires every scenario customer to
 * have one active roster and card binding. Provider values are read from the
 * audited operator-managed database tables; no IDs are inferred or seeded.
 */
export function createKit5ScenarioResolver(pool: DatabasePool) {
  return async (scenarioId: string): Promise<Kit5CustomerFixture | null> => {
    const specs = resolveScenarioSpecs(scenarioId);
    if (!specs) return null;
    const bindings = await loadBindings(pool, specs.map(({ customerId }) => customerId));
    if (!bindings) return null;
    const byCustomer = new Map(bindings.map((binding) => [binding.customerId, binding]));
    const customers = new Map(specs.map((spec) => {
      const binding = byCustomer.get(spec.customerId);
      if (!binding) return [spec.customerId, null] as const;
      return [spec.customerId, {
        accountId: binding.accountId,
        cardId: binding.cardId,
        amountCents: spec.amountCents,
        currency: "USD",
        merchant: spec.merchant,
        merchantCategoryCode: spec.merchantCategoryCode,
      }] as const;
    }));
    if ([...customers.values()].some((customer) => customer === null)) return null;
    return {
      orderedCustomerIds: specs.map(({ customerId }) => customerId),
      resolveCustomer: (customerId) => customers.get(customerId) ?? null,
      policyFacts: (customerId) => {
        const spec = specs.find((candidate) => candidate.customerId === customerId);
        return spec ? {
          transactionType: "AUTHORIZATION",
          transactionCategory: "PURCHASE",
          merchantCategoryCode: spec.merchantCategoryCode,
        } : null;
      },
    };
  };
}

/**
 * Resolves the one server-approved provider checkpoint. The caller supplies
 * only its run identity; roster/card bindings and transaction terms come from
 * the existing trusted scenario resolver.
 */
export function createKit5FixtureCheckpointResolver(pool: DatabasePool) {
  return async (input: NextCustomerInput): Promise<Kit5CustomerFixture | null> => {
    if (!isKit5FixtureCheckpointInput(input)) return null;
    const specs = SCENARIOS[KIT5_TARGET_SCENARIO_ID];
    const bloomSpec = specs?.find((spec) => spec.customerId === KIT5_BLOOM_CUSTOMER_ID);
    if (!specs || specs[0]?.customerId !== KIT5_BLOOM_CUSTOMER_ID || !bloomSpec ||
        bloomSpec.amountCents !== KIT5_BLOOM_CHECKPOINT_AMOUNT_CENTS ||
        bloomSpec.merchant !== "Supplier" || bloomSpec.merchantCategoryCode !== "5411") return null;

    // Only Bloom needs an Airwallex binding for this provider checkpoint.
    // Keep the complete scenario order for the durable first-attempt guard.
    const bindings = await loadBindings(pool, [KIT5_BLOOM_CUSTOMER_ID]);
    const bloomBinding = bindings?.[0];
    if (!bloomBinding) return null;
    const customer = {
      accountId: bloomBinding.accountId,
      cardId: bloomBinding.cardId,
      amountCents: bloomSpec.amountCents,
      currency: "USD",
      merchant: bloomSpec.merchant,
      merchantCategoryCode: bloomSpec.merchantCategoryCode,
    };
    return {
      orderedCustomerIds: specs.map(({ customerId }) => customerId),
      resolveCustomer: (customerId) => customerId === KIT5_BLOOM_CUSTOMER_ID ? customer : null,
      policyFacts: (customerId) => customerId === KIT5_BLOOM_CUSTOMER_ID ? {
        transactionType: "AUTHORIZATION",
        transactionCategory: "PURCHASE",
        merchantCategoryCode: bloomSpec.merchantCategoryCode,
      } : null,
    };
  };
}

/** Nova's repayment target is server-only, USD-only, and disabled without an operator-supplied Global Account ID. */
export function createKit5RepaymentDepositTargetResolver(
  pool: DatabasePool,
  config: {
    globalAccountId?: string;
    connectedAccountId?: string;
    currency?: "USD";
  },
) {
  return async (scenarioId: string, customerId: string): Promise<Kit5DepositTarget | null> => {
    if (!resolveScenarioSpecs(scenarioId)?.some((customer) => customer.customerId === NOVA_CUSTOMER_ID) ||
        customerId !== NOVA_CUSTOMER_ID || !isProviderId(config.globalAccountId) ||
        config.globalAccountId === customerId || !isProviderId(config.connectedAccountId) ||
        config.currency !== "USD") return null;
    const bindings = await loadBindings(pool, [NOVA_CUSTOMER_ID]);
    const binding = bindings?.[0];
    if (!binding || binding.accountId !== config.connectedAccountId) return null;
    return { accountId: binding.accountId, globalAccountId: config.globalAccountId };
  };
}

async function loadBindings(pool: DatabasePool, customerIds: readonly string[]): Promise<ResolvedBinding[] | null> {
  if (customerIds.length === 0 || new Set(customerIds).size !== customerIds.length) return null;
  let result;
  try {
    result = await pool.query<BindingRow>(
      `SELECT roster.customer_id, roster.airwallex_connected_account_id AS roster_account_id,
              roster.is_active AS roster_active, binding.card_id,
              binding.airwallex_account_id AS binding_account_id,
              binding.is_active AS binding_active
         FROM customer_roster AS roster
         LEFT JOIN issuing_card_bindings AS binding ON binding.customer_id = roster.customer_id
        WHERE roster.customer_id = ANY($1::text[])
        ORDER BY roster.customer_id, binding.card_id`,
      [customerIds],
    );
  } catch {
    return null;
  }
  const rowsByCustomer = new Map<string, BindingRow[]>();
  for (const row of result.rows) {
    const rows = rowsByCustomer.get(row.customer_id) ?? [];
    rows.push(row);
    rowsByCustomer.set(row.customer_id, rows);
  }
  const resolved: ResolvedBinding[] = [];
  for (const customerId of customerIds) {
    const rows = rowsByCustomer.get(customerId) ?? [];
    if (rows.length !== 1) return null;
    const row = rows[0];
    if (!row || row.roster_active !== true || row.binding_active !== true ||
        !isProviderId(row.roster_account_id) || !isProviderId(row.binding_account_id) ||
        !isProviderId(row.card_id) || row.roster_account_id === customerId ||
        row.binding_account_id !== row.roster_account_id) return null;
    resolved.push({ customerId, accountId: row.roster_account_id, cardId: row.card_id });
  }
  return resolved;
}

function isProviderId(value: unknown): value is string {
  return typeof value === "string" && SAFE_PROVIDER_ID.test(value.trim()) && value === value.trim();
}
