import { useRef, useState } from 'react';
import type { BrowserSession } from '../../../services/browser-session/client.js';
import { ProductReadClient } from '../../../services/financial/product-reads.js';
import type { CaseDetail, CasePage } from '../../../services/financial/client.js';
import { cents } from '../../../services/financial/client.js';
import type { ProductBridge } from '../../../services/financial/product-models.js';
import { GlowBackground } from '../ui/Surface.js';
import { useProductQuery } from '../ui/useProductQuery.js';
import { BridgeCard, OfferCard, Recovery, ReadNotice, SourceNote } from '../ui/ConnectedFinance.js';
import { financeRead, financeCommand, human, stateTone } from '../ui/finance.js';
import { color, font, space, radius, glow } from './tokens.js';
import {
  Badge,
  Button,
  Card,
  CardHeader,
  ClickableTr,
  Empty,
  Row,
  Stat,
  Table,
  Td,
  Th,
  Breadcrumb,
} from './components/primitives.js';
import { ApplicationsScreen } from './screens/ApplicationsScreen.js';
import { ApplicationDetailScreen } from './screens/ApplicationDetailScreen.js';
import { OnboardingWizard } from '../../../components/onboarding/OnboardingWizard.js';
import type { ApplicationsListState, View } from './useBackOffice.js';
const product = new ProductReadClient();
const labels: Record<string, string> = {
  acct_bloom001: 'Bloom (demo)',
  acct_atlas001: 'Atlas (demo)',
  acct_nova001: 'Nova (demo)',
};
const customerLabel = (id: string | null) => (id ? (labels[id] ?? id) : 'Customer unavailable');
type ProductView = {
  screen:
    | 'overview'
    | 'applications'
    | 'application'
    | 'draft'
    | 'customers'
    | 'customer'
    | 'request'
    | 'bridge'
    | 'offer'
    | 'rules';
  id?: string;
};
const titles: Record<ProductView['screen'], string> = {
  overview: 'Bridge Capital',
  applications: 'Applications',
  application: 'Application',
  draft: 'New company draft',
  customers: 'Customers',
  customer: 'Customer',
  request: 'Request',
  bridge: 'Bridge',
  offer: 'Offer',
  rules: 'Rules and thresholds',
};
export interface CaseRow {
  kind: string;
  id: string;
  customer_id: string | null;
  status: string;
  decision_status: string | null;
  updated_at: string;
  detail: CaseDetail | null;
  error: string;
}
async function loadCasePage(
  cursor?: string
): Promise<{ items: CaseRow[]; next_cursor: string | null }> {
  const page: CasePage = await financeRead.cases(cursor);
  const items: CaseRow[] = page.items.map((x) => ({ ...x, detail: null, error: '' }));
  let next = 0;
  await Promise.all(
    Array.from({ length: Math.min(4, items.length) }, async () => {
      while (next < items.length) {
        const index = next++;
        try {
          items[index].detail = await financeRead.case(items[index].id);
        } catch (e) {
          items[index].error = e instanceof Error ? e.message : 'Assessment unavailable';
        }
      }
    })
  );
  return { items, next_cursor: page.next_cursor };
}
async function caseOffers(caseID: string) {
  const ids = new Set<string>();
  let after = 0;
  for (let page = 0; page < 20; page++) {
    const feed = await financeRead.changes(after);
    for (const item of feed.items)
      if (item.originating_case_id === caseID && item.offer_id) ids.add(item.offer_id);
    if (!feed.has_more) return [...ids];
    after = feed.next_after_seq;
  }
  throw new Error(
    'More servicing history is available. Continue discovery in the developer interface.'
  );
}
const ruleNames: Record<string, string> = {
  INPUT_FRESH: 'Inputs are current',
  CUSTOMER_ACTIVE: 'Customer standing',
  HISTORY_SUFFICIENT: 'Sufficient history',
  NO_OVERDUE_OR_DEFAULT: 'Repayment history',
  CUSTOMER_CAPACITY: 'Customer capacity',
  PAYMENT_NOT_BRIDGED: 'Payment is not already bridged',
  RECEIVABLE_COVERAGE: 'Receivable coverage',
  CAPITAL_AVAILABLE: 'Facility capacity',
  SPEND_ALLOWED: 'Spend category',
  REGIME_TIER_ALLOWED: 'Tier and capital regime',
};
export function SavedRules({ value }: { value: CaseDetail }) {
  return (
    <Card>
      <CardHeader
        title="Policy checks"
        meta={
          value.assessment?.status && (
            <Badge tone={stateTone(value.assessment.status)}>
              {human(value.assessment.status)}
            </Badge>
          )
        }
      />
      {value.assessment?.rules.map((rule) => (
        <div
          key={rule.rule_id}
          style={{ padding: '16px 0', borderTop: `1px solid ${color.border}` }}
        >
          <div style={{ display: 'flex', gap: 12, justifyContent: 'space-between' }}>
            <span>{ruleNames[rule.rule_id] ?? human(rule.rule_id)}</span>
            <Badge
              tone={
                rule.status === 'PASS'
                  ? 'positive'
                  : rule.status === 'FAIL'
                    ? 'critical'
                    : 'warning'
              }
            >
              {rule.status === 'UNKNOWN' ? 'Needs information' : human(rule.status)}
            </Badge>
          </div>
          <p style={{ color: color.textMuted, fontSize: 13 }}>{human(rule.reason_code)}</p>
          <details style={{ fontSize: 12, color: color.textMuted }}>
            <summary>Evidence and threshold</summary>
            <p style={{ overflowWrap: 'anywhere' }}>
              Observed: {JSON.stringify(rule.actual)}
              <br />
              Threshold: {JSON.stringify(rule.threshold)}
            </p>
          </details>
        </div>
      ))}
      {!value.assessment && <Empty>Assessment is not available yet.</Empty>}
    </Card>
  );
}

export function ConnectedBackOffice({
  session,
  logout,
  relogin,
  startOnboarding = false,
}: {
  session: BrowserSession;
  logout: () => void;
  relogin: () => void;
  startOnboarding?: boolean;
}) {
  const [view, setView] = useState<ProductView>({ screen: startOnboarding ? 'draft' : 'overview' });
  const [listState, setListState] = useState<ApplicationsListState>({ previousCursors: [] });
  const portfolio = useProductQuery('portfolio', () => product.portfolio(), 5000);
  const go = (next: ProductView) => setView(next);
  function goApplication(next: View) {
    if (next.screen === 'application') {
      setListState(next.listState);
      go({ screen: 'application', id: next.applicationId });
    } else if (next.screen === 'applications') {
      setListState(next.listState);
      go({ screen: 'applications' });
    }
  }
  const nav = [
    ['overview', 'Overview'],
    ['applications', 'Applications'],
    ['customers', 'Customers'],
    ['rules', 'Rules'],
    ['developer', 'Admin'],
  ] as const;
  return (
    <GlowBackground
      canvas={color.bg}
      glow={glow}
      style={{
        display: 'flex',
        color: color.text,
        fontFamily: font.sans,
        fontSize: font.size.md,
        minHeight: '100vh',
      }}
    >
      <aside style={{ width: 232, flexShrink: 0, display: 'flex', flexDirection: 'column' }}>
        <div
          style={{
            padding: `${space['3xl']} ${space['2xl']} ${space['2xl']}`,
            display: 'grid',
            gap: space.xs,
          }}
        >
          <div style={{ fontSize: font.size.xl, fontWeight: font.weight.semibold }}>FlowCard</div>
          <div style={{ color: color.textMuted }}>Bridge Capital</div>
        </div>
        <nav
          style={{
            padding: `0 ${space.md}`,
            display: 'grid',
            gap: 2,
            alignContent: 'start',
            flex: 1,
          }}
        >
          {nav.map(([screen, label]) => (
            <button
              key={screen}
              type="button"
              onClick={() =>
                screen === 'developer' ? (window.location.hash = '#developer') : go({ screen })
              }
              style={{
                width: '100%',
                textAlign: 'left',
                background:
                  view.screen === screen ||
                  (screen === 'applications' && view.screen === 'application')
                    ? color.sidebarActiveBg
                    : 'transparent',
                color: view.screen === screen ? color.sidebarTextActive : color.sidebarText,
                border: 'none',
                borderRadius: radius.lg,
                padding: `${space.lg} ${space.xl}`,
                fontFamily: font.sans,
                fontSize: font.size.lg,
                cursor: 'pointer',
              }}
            >
              {label}
            </button>
          ))}
        </nav>
        <div style={{ padding: 20, color: color.textMuted, fontSize: 13 }}>
          {session.user.username}
          <br />
          <button
            type="button"
            onClick={relogin}
            style={{
              background: 'none',
              border: 0,
              color: color.textMuted,
              padding: '12px 0',
              cursor: 'pointer',
            }}
          >
            Sign in again
          </button>
          <br />
          <button
            type="button"
            onClick={logout}
            style={{
              background: 'none',
              border: 0,
              color: color.textMuted,
              padding: '16px 0',
              cursor: 'pointer',
            }}
          >
            Sign out
          </button>
        </div>
      </aside>
      <div style={{ flex: 1, minWidth: 0 }}>
        <header
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            padding: `${space['2xl']} ${space['3xl']}`,
          }}
        >
          <h1 style={{ fontSize: font.size['2xl'], margin: 0 }}>{titles[view.screen]}</h1>
          <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
            {portfolio.data?.current_regime && (
              <Badge tone="neutral">{human(portfolio.data.current_regime)}</Badge>
            )}
            <span style={{ fontSize: 12, color: color.textMuted }}>DEMO · Go services</span>
          </div>
        </header>
        <main style={{ padding: `0 ${space['3xl']} ${space['4xl']}`, display: 'grid', gap: 20 }}>
          {view.screen === 'overview' && <OperatorOverview portfolio={portfolio} go={go} />}
          {view.screen === 'customers' && <OperatorCustomers go={go} />}
          {view.screen === 'customer' && view.id && <OperatorCustomer id={view.id} go={go} />}
          {view.screen === 'request' && view.id && <OperatorRequest id={view.id} go={go} />}
          {view.screen === 'bridge' && view.id && (
            <>
              <Breadcrumb
                items={[
                  { label: 'Customers', onClick: () => go({ screen: 'customers' }) },
                  { label: 'Bridge' },
                ]}
              />
              <BridgeCard key={view.id} id={view.id} operator />
              <Recovery />
            </>
          )}
          {view.screen === 'offer' && view.id && (
            <>
              <OfferCard
                key={view.id}
                id={view.id}
                session={session}
                openBridge={(id) => go({ screen: 'bridge', id })}
              />
              <Recovery />
            </>
          )}
          {view.screen === 'rules' && <RulesEvidence />}
          {view.screen === 'applications' && (
            <>
              <div>
                <Button onClick={() => go({ screen: 'draft' })}>New company draft</Button>
              </div>
              <ApplicationsScreen listState={listState} go={goApplication} />
            </>
          )}
          {view.screen === 'application' && view.id && (
            <ApplicationDetailScreen
              applicationId={view.id}
              listState={listState}
              go={goApplication}
            />
          )}
          {view.screen === 'draft' && (
            <OnboardingWizard onComplete={() => go({ screen: 'applications' })} />
          )}
          <SourceNote mode={portfolio.data?.source_mode} />
        </main>
      </div>
    </GlowBackground>
  );
}

function OperatorOverview({
  portfolio,
  go,
}: {
  portfolio: ReturnType<
    typeof useProductQuery<Awaited<ReturnType<ProductReadClient['portfolio']>>>
  >;
  go: (v: ProductView) => void;
}) {
  const [cursor, setCursor] = useState<string>();
  const [previous, setPrevious] = useState<Array<string | undefined>>([]);
  const queue = useProductQuery('queue:' + cursor, () => loadCasePage(cursor), 5000);
  const data = portfolio.data;
  return (
    <>
      <ReadNotice {...portfolio} />
      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,2fr) minmax(0,1fr)', gap: 20 }}>
        <Card>
          <CardHeader
            title="Facility"
            meta={data?.current_regime && <Badge>{human(data.current_regime)}</Badge>}
          />
          <Stat label="Total capital" value={cents(data?.facility_total_cents)} />
          {data && (
            <>
              <div
                style={{
                  display: 'flex',
                  height: 10,
                  borderRadius: 20,
                  overflow: 'hidden',
                  margin: '24px 0',
                  background: color.surfaceSubtle,
                }}
              >
                {[
                  [data.funded_principal_cents, color.accent],
                  [data.reserved_principal_cents + data.other_commitments_cents, color.warning],
                  [data.available_capital_cents ?? 0, color.positive],
                  [data.reserve_floor_cents ?? 0, color.borderStrong],
                ].map(([amount, fill], i) => (
                  <div
                    key={i}
                    style={{
                      width: `${Math.max(0, Math.min(100, (Number(amount) / data.facility_total_cents) * 100))}%`,
                      background: String(fill),
                    }}
                  />
                ))}
              </div>
              <div style={{ display: 'flex', gap: 22, flexWrap: 'wrap' }}>
                <span>Deployed {cents(data.funded_principal_cents)}</span>
                <span>Reserved {cents(data.reserved_principal_cents)}</span>
                <span>Available {cents(data.available_capital_cents)}</span>
                <span>Reserve floor {cents(data.reserve_floor_cents)}</span>
              </div>
              <small style={{ display: 'block', marginTop: 20, color: color.textMuted }}>
                Facility version {data.facility_version} · as of{' '}
                {new Date(data.as_of).toLocaleTimeString()}
              </small>
            </>
          )}
        </Card>
        <Card>
          <Stat label="Outstanding principal" value={cents(data?.principal_owed_cents)} />
          <div style={{ marginTop: 25 }}>
            <Stat
              label="Fees collected"
              value={cents(data?.fees_collected_cents)}
              sub={
                data?.history_complete
                  ? 'Settled collections, net of reversals'
                  : 'Opening history is incomplete'
              }
            />
          </div>
          <p style={{ color: color.textMuted }}>Bridges by stage</p>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            {Object.entries(data?.lifecycle_counts ?? {}).map(([status, count]) => (
              <Badge key={status} tone={stateTone(status)}>
                {human(status)} · {count}
              </Badge>
            ))}
            {data && Object.keys(data.lifecycle_counts).length === 0 && <span>No bridges yet</span>}
          </div>
        </Card>
      </div>
      <Card padded={false}>
        <div style={{ padding: '24px 24px 0' }}>
          <CardHeader title="Request queue" meta="Saved assessments · newest first" />
          <ReadNotice {...queue} />
        </div>
        {queue.data?.items.length === 0 && <Empty>No cases yet.</Empty>}
        <Table>
          <thead>
            <tr>
              <Th>Customer</Th>
              <Th>Tier</Th>
              <Th>Declined payment</Th>
              <Th align="right">Shortfall</Th>
              <Th align="right">Limit</Th>
              <Th>Assessment</Th>
            </tr>
          </thead>
          <tbody>
            {queue.data?.items.map((row) => (
              <ClickableTr
                key={row.id}
                onClick={() =>
                  row.kind === 'COLLECTION' && row.detail?.bridge_id
                    ? go({ screen: 'bridge', id: row.detail.bridge_id })
                    : go({ screen: 'request', id: row.id })
                }
              >
                <Td>
                  <button
                    type="button"
                    style={{
                      background: 'none',
                      border: 0,
                      color: color.text,
                      cursor: 'pointer',
                      padding: 0,
                    }}
                  >
                    {customerLabel(row.customer_id)}
                  </button>
                </Td>
                <Td>{row.detail?.assessment?.tier ?? 'Unavailable'}</Td>
                <Td>
                  {row.kind === 'COLLECTION' ? 'Bridge monitoring' : 'Saved shortfall event'}
                  <small style={{ display: 'block', color: color.textMuted }}>
                    Merchant details unavailable
                  </small>
                </Td>
                <Td align="right">{cents(row.detail?.assessment?.principal_cents)}</Td>
                <Td align="right">{cents(row.detail?.assessment?.customer_limit_cents)}</Td>
                <Td>
                  <Badge
                    tone={stateTone(
                      row.detail?.assessment?.status ?? row.decision_status ?? row.status
                    )}
                  >
                    {human(row.detail?.assessment?.status ?? row.decision_status ?? row.status)}
                  </Badge>
                  {row.error && <small>{row.error}</small>}
                </Td>
              </ClickableTr>
            ))}
          </tbody>
        </Table>
        <div style={{ padding: 24, display: 'flex', gap: 12 }}>
          <Button
            disabled={!previous.length}
            onClick={() => {
              const old = [...previous];
              setCursor(old.pop());
              setPrevious(old);
            }}
          >
            Previous cases
          </Button>
          <Button
            disabled={!queue.data?.next_cursor}
            onClick={() => {
              setPrevious((x) => [...x, cursor]);
              setCursor(queue.data?.next_cursor ?? undefined);
            }}
          >
            Next cases
          </Button>
        </div>
      </Card>
    </>
  );
}
function OperatorCustomers({ go }: { go: (v: ProductView) => void }) {
  const [cursor, setCursor] = useState<string>();
  const [previous, setPrevious] = useState<Array<string | undefined>>([]);
  const rows = useProductQuery('customers:' + cursor, () => product.customers(cursor));
  return (
    <Card padded={false}>
      <div style={{ padding: 24 }}>
        <CardHeader title="Customer portfolio" meta="Verified servicing mappings" />
        <ReadNotice {...rows} />
      </div>
      <Table>
        <thead>
          <tr>
            <Th>Customer</Th>
            <Th>Account</Th>
            <Th align="right">Principal owed</Th>
            <Th align="right">Unpaid fees</Th>
            <Th>History</Th>
          </tr>
        </thead>
        <tbody>
          {rows.data?.items.map((c) => (
            <ClickableTr
              key={c.customer_id}
              onClick={() => go({ screen: 'customer', id: c.customer_id })}
            >
              <Td>
                <span>{c.display_name ?? c.customer_id}</span>
              </Td>
              <Td>
                <Badge tone={c.mapping_active ? 'positive' : 'warning'}>
                  {c.mapping_active ? 'Active mapping' : 'Inactive mapping'}
                </Badge>
              </Td>
              <Td align="right">{cents(c.principal_owed_cents)}</Td>
              <Td align="right">{cents(c.unpaid_fee_cents)}</Td>
              <Td>
                {!c.accounting_available
                  ? 'Accounting unavailable'
                  : c.history_complete
                    ? 'Complete'
                    : 'Partial'}
              </Td>
            </ClickableTr>
          ))}
        </tbody>
      </Table>
      <div style={{ padding: 24, display: 'flex', gap: 12 }}>
        <Button
          disabled={!previous.length}
          onClick={() => {
            const p = [...previous];
            setCursor(p.pop());
            setPrevious(p);
          }}
        >
          Previous customers
        </Button>
        <Button
          disabled={!rows.data?.next_cursor}
          onClick={() => {
            setPrevious((x) => [...x, cursor]);
            setCursor(rows.data?.next_cursor ?? undefined);
          }}
        >
          Next customers
        </Button>
      </div>
    </Card>
  );
}
function BridgeRows({ items, open }: { items: ProductBridge[]; open: (id: string) => void }) {
  return (
    <Table>
      <thead>
        <tr>
          <Th>Status</Th>
          <Th>Original principal</Th>
          <Th>Contract fee</Th>
          <Th>Due</Th>
          <Th>Principal owed</Th>
        </tr>
      </thead>
      <tbody>
        {items.map((b) => (
          <ClickableTr key={b.id} onClick={() => open(b.id)}>
            <Td>
              <Badge tone={stateTone(b.lifecycle)}>{human(b.lifecycle)}</Badge>
            </Td>
            <Td>{cents(b.principal_original_cents)}</Td>
            <Td>{cents(b.contract_fee_original_cents)}</Td>
            <Td>{b.due_on}</Td>
            <Td>{cents(b.principal_owed_cents)}</Td>
          </ClickableTr>
        ))}
      </tbody>
    </Table>
  );
}
function OperatorCustomer({ id, go }: { id: string; go: (v: ProductView) => void }) {
  const customer = useProductQuery('customer:' + id, () => product.customer(id));
  const [cursor, setCursor] = useState<string>();
  const bridges = useProductQuery(
    'customer-bridges:' + id + ':' + cursor,
    () => product.bridges(id, cursor),
    5000
  );
  const c = customer.data?.customer;
  return (
    <>
      <Breadcrumb
        items={[
          { label: 'Customers', onClick: () => go({ screen: 'customers' }) },
          { label: c?.display_name ?? id },
        ]}
      />
      <Card>
        <CardHeader title={c?.display_name ?? id} />
        <ReadNotice {...customer} />
        {c && (
          <>
            <Row label="Principal owed" value={cents(c.principal_owed_cents)} />
            <Row label="Unpaid fees" value={cents(c.unpaid_fee_cents)} />
            <Row label="Reserved principal" value={cents(c.reserved_principal_cents)} />
            <Row label="Reserved fees" value={cents(c.reserved_fee_cents)} />
            <Row
              label="History"
              value={c.history_complete ? 'Complete' : 'Unavailable or partial'}
            />
            <p>
              Tier and credit line are recorded on individual saved assessments. Current funds
              require a fresh service check.
            </p>
          </>
        )}
      </Card>
      <Card>
        <CardHeader title="Bridge history" />
        <ReadNotice {...bridges} />
        {bridges.data && (
          <BridgeRows items={bridges.data.items} open={(id) => go({ screen: 'bridge', id })} />
        )}
        <Button
          disabled={!bridges.data?.next_cursor}
          onClick={() => setCursor(bridges.data?.next_cursor ?? undefined)}
        >
          Older bridges
        </Button>
        {cursor && <Button onClick={() => setCursor(undefined)}>Newest bridges</Button>}
      </Card>
    </>
  );
}
function OperatorRequest({ id, go }: { id: string; go: (v: ProductView) => void }) {
  const detail = useProductQuery('case:' + id, () => financeRead.case(id), 5000);
  const offers = useProductQuery('case-offers:' + id, () => caseOffers(id), 5000);
  const history = useProductQuery('case-history:' + id, () => financeCommand.history(), 5000);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [attempted, setAttempted] = useState(false);
  const createFlight = useRef(false);
  const value = detail.data;
  const a = value?.assessment;
  const unresolved =
    history.data?.some(
      (c) =>
        c.state === 'UNKNOWN' && c.path === '/api/offers' && c.payload.originating_case_id === id
    ) ?? true;
  return (
    <>
      <Breadcrumb
        items={[
          { label: 'Overview', onClick: () => go({ screen: 'overview' }) },
          { label: customerLabel(value?.customer_id ?? null) },
        ]}
      />
      <ReadNotice {...detail} />
      {value && (
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'minmax(0,1.25fr) minmax(0,1fr)',
            gap: 20,
            alignItems: 'start',
          }}
        >
          <div style={{ display: 'grid', gap: 20 }}>
            <Card>
              <CardHeader
                title="Triggering decline"
                meta={
                  <Badge tone={stateTone(a?.status ?? value.status)}>
                    {human(a?.status ?? value.status)}
                  </Badge>
                }
              />
              <Stat label="Shortfall" value={cents(a?.principal_cents)} />
              <Row label="Customer" value={customerLabel(value.customer_id)} />
              <Row label="Assessment captured" value={a?.evaluated_at ?? 'Pending'} />
              <p style={{ color: color.textMuted }}>
                Merchant, full payment amount and wallet details are not provided by this saved case
                view.
              </p>
            </Card>
            <SavedRules value={value} />
            <Card>
              <CardHeader title="Offer and approval" />
              {offers.data?.map((offer) => (
                <div key={offer} style={{ margin: '10px 0' }}>
                  <Button onClick={() => go({ screen: 'offer', id: offer })}>
                    Review saved offer
                  </Button>
                </div>
              ))}
              {offers.error && <p role="alert">{offers.error}</p>}
              {offers.data?.length === 0 && (
                <>
                  <p>
                    Create fixed terms from this exact saved assessment. The service rechecks
                    eligibility and freshness.
                  </p>
                  <Button
                    variant="primary"
                    disabled={
                      busy ||
                      detail.busy ||
                      !!detail.error ||
                      !!history.error ||
                      attempted ||
                      unresolved ||
                      !!offers.error ||
                      !a ||
                      !['ELIGIBLE', 'REVIEW_REQUIRED'].includes(a.status) ||
                      !a.rules.every((r) => r.status === 'PASS')
                    }
                    onClick={() => {
                      if (createFlight.current) return;
                      createFlight.current = true;
                      setBusy(true);
                      setAttempted(true);
                      setError('');
                      void financeCommand
                        .create(value)
                        .then((receipt) => {
                          offers.refresh();
                          history.refresh();
                          if (typeof receipt.receipt.id === 'string')
                            go({ screen: 'offer', id: receipt.receipt.id });
                        })
                        .catch((e) => {
                          setError(e.message);
                          history.refresh();
                        })
                        .finally(() => setBusy(false));
                    }}
                  >
                    Create bridge offer
                  </Button>
                </>
              )}
              {error && <p role="alert">{error}</p>}
              {unresolved && <p>Check saved actions before creating a new offer.</p>}
            </Card>
          </div>
          <div style={{ display: 'grid', gap: 20 }}>
            <Card>
              <CardHeader title="Customer capacity" />
              <Row label="Assessed credit limit" value={cents(a?.customer_limit_cents)} />
              <Row label="Exposure at assessment" value={cents(a?.existing_exposure_cents)} />
              <Row label="Exposure after proposal" value={cents(a?.post_funding_exposure_cents)} />
              <Row label="Fee estimate" value={cents(a?.estimated_fee_cents)} />
              <p>These values describe the saved assessment. Funding uses current ledger facts.</p>
            </Card>
            <Card>
              <CardHeader title="Investigation commentary" />
              {value.investigation ? (
                <>
                  <p>{value.investigation.summary}</p>
                  <Badge tone="warning">
                    {value.investigation.verified ? 'Verified evidence' : 'Unverified commentary'}
                  </Badge>
                  <p style={{ fontSize: 12, color: color.textMuted }}>
                    Mode {value.investigation.explanation_mode} ·{' '}
                    {value.investigation.current ? 'Current' : 'Stale'} commentary
                  </p>
                </>
              ) : (
                <p>No investigation commentary available.</p>
              )}
            </Card>
            <Card>
              <CardHeader title="Decision provenance" />
              <p style={{ overflowWrap: 'anywhere', fontSize: 12 }}>
                {a?.policy_id}
                <br />
                {a?.policy_hash}
              </p>
              <p>Assessment is non-authorizing. Funding requires recheck.</p>
            </Card>
          </div>
        </div>
      )}
      <Recovery
        changed={() => {
          offers.refresh();
          history.refresh();
        }}
      />
    </>
  );
}
function RulesEvidence() {
  const cases = useProductQuery('rules-cases', () => loadCasePage());
  const value = cases.data?.items.find((x) => x.detail?.assessment)?.detail;
  return (
    <>
      <Card>
        <CardHeader title="Rules and thresholds" meta="Read-only saved policy evidence" />
        <ReadNotice {...cases} />
        <p>
          Each assessment preserves its rule outcomes, observed values, thresholds, policy version
          and source snapshots. Browser controls cannot change this policy.
        </p>
        {value?.assessment && (
          <p style={{ overflowWrap: 'anywhere', fontSize: 12 }}>
            {value.assessment.policy_id} · {value.assessment.policy_hash}
            <br />
            Evaluated {value.assessment.evaluated_at} · {customerLabel(value.customer_id)}
          </p>
        )}
      </Card>
      {value && <SavedRules value={value} />}
    </>
  );
}
