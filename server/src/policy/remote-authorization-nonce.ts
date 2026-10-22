import { createHash } from "node:crypto";
import type { DatabasePool } from "../db/pool.js";
import {
  fingerprintRemoteAuthorizationRequest,
  type RemoteAuthorizationNonceClaimer,
} from "../http/routes/remote-authorization.js";

/** Persist an irreversible hash claim for every authenticated provider nonce. */
export function createRemoteAuthorizationNonceClaimer(pool: Pick<DatabasePool, "query">): RemoteAuthorizationNonceClaimer {
  return async (request, nonce, receivedAt) => {
    const nonceFingerprint = createHash("sha256").update(nonce, "utf8").digest("hex");
    const requestFingerprint = fingerprintRemoteAuthorizationRequest(request);
    const inserted = await pool.query(
      `INSERT INTO remote_authorization_nonce_claims (
         nonce_fingerprint, request_fingerprint, card_transaction_event_id, claimed_at
       ) VALUES ($1, $2, $3, $4)
       ON CONFLICT (nonce_fingerprint) DO NOTHING
       RETURNING nonce_fingerprint`,
      [nonceFingerprint, requestFingerprint, request.cardTransactionEventId, receivedAt],
    );
    if (inserted.rowCount === 1) return "claimed";

    const prior = await pool.query<{ request_fingerprint: string; card_transaction_event_id: string }>(
      `SELECT request_fingerprint, card_transaction_event_id
         FROM remote_authorization_nonce_claims WHERE nonce_fingerprint = $1`,
      [nonceFingerprint],
    );
    const row = prior.rows[0];
    return row?.request_fingerprint === requestFingerprint &&
      row.card_transaction_event_id === request.cardTransactionEventId
      ? "exact_retry"
      : "replay_mismatch";
  };
}
