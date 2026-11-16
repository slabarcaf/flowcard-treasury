package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

const productPageSize = 25

type productSource struct {
	Mode     string    `json:"source_mode"`
	Binding  string    `json:"source_binding_id"`
	Facility string    `json:"facility_id"`
	AsOf     time.Time `json:"as_of"`
}
type productCursor struct {
	Mode     string `json:"mode"`
	Binding  string `json:"binding"`
	Facility string `json:"facility"`
	Route    string `json:"route"`
	Customer string `json:"customer"`
	After    string `json:"after"`
	Created  string `json:"created"`
}

func (h *handler) productSource() productSource {
	return productSource{h.cfg.Mode, h.cfg.SourceBindingID, h.cfg.FacilityID, h.now().UTC()}
}
func (h *handler) productActor(w http.ResponseWriter, r *http.Request, operator bool) (Identity, bool) {
	id, ok := h.auth(w, r, false)
	if !ok {
		return id, false
	}
	if id.Role != "ACTOR" || (id.ActorType != "OPERATOR" && id.ActorType != "CUSTOMER") || (operator && id.ActorType != "OPERATOR") {
		writeError(w, 403, "FORBIDDEN", "Scope does not allow this read")
		return id, false
	}
	return id, true
}
func strictProductQuery(r *http.Request, allowed ...string) (url.Values, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	for k, v := range q {
		valid := false
		for _, a := range allowed {
			if k == a {
				valid = true
			}
		}
		if !valid || len(v) != 1 || v[0] == "" {
			return nil, errors.New("invalid query")
		}
	}
	return q, nil
}
func (h *handler) productCursor(r *http.Request, scope string, q url.Values) (productCursor, error) {
	c := productCursor{Mode: h.cfg.Mode, Binding: h.cfg.SourceBindingID, Facility: h.cfg.FacilityID, Route: r.Pattern, Customer: scope}
	raw := q.Get("cursor")
	if raw == "" {
		return c, nil
	}
	if len(raw) > 2048 {
		return c, errors.New("invalid cursor")
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return c, err
	}
	var got productCursor
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&got) != nil {
		return c, errors.New("invalid cursor")
	}
	encoded, _ := json.Marshal(got)
	if !bytes.Equal(encoded, data) || got.Mode != c.Mode || got.Binding != c.Binding || got.Facility != c.Facility || got.Route != c.Route || got.Customer != c.Customer || got.After == "" {
		return c, errors.New("invalid cursor")
	}
	return got, nil
}
func encodeProductCursor(c productCursor) *string {
	b, _ := json.Marshal(c)
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}
func productUnavailable(w http.ResponseWriter) {
	writeError(w, 503, "STORAGE_UNAVAILABLE", "Product records are unavailable")
}
func validProductCustomer(id string) bool {
	return id != "" && len(id) <= 128 && strings.TrimSpace(id) == id && !strings.ContainsAny(id, "/\\\x00")
}
func demoCustomerLabel(mode, id string) *string {
	if mode != "DEMO" {
		return nil
	}
	labels := map[string]string{"acct_bloom001": "Bloom (demo)", "acct_atlas001": "Atlas (demo)", "acct_nova001": "Nova (demo)"}
	if label, ok := labels[id]; ok {
		return &label
	}
	return nil
}

type portfolioDTO struct {
	productSource
	Version    int64                 `json:"facility_version"`
	Total      int64                 `json:"facility_total_cents"`
	ReserveBPS int64                 `json:"reserve_bps"`
	Floor      *int64                `json:"reserve_floor_cents"`
	Funded     int64                 `json:"funded_principal_cents"`
	Reserved   int64                 `json:"reserved_principal_cents"`
	Other      int64                 `json:"other_commitments_cents"`
	Available  *int64                `json:"available_capital_cents"`
	Regime     *policy.CapitalRegime `json:"current_regime"`
	Principal  int64                 `json:"principal_owed_cents"`
	Fees       int64                 `json:"unpaid_fee_cents"`
	Collected  *int64                `json:"fees_collected_cents"`
	Complete   bool                  `json:"history_complete"`
	Counts     map[string]int64      `json:"lifecycle_counts"`
	Held       int64                 `json:"held_bridge_count"`
	PolicyID   string                `json:"policy_id"`
	PolicyHash string                `json:"policy_hash"`
}

func (h *handler) readPortfolio(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.productActor(w, r, true); !ok {
		return
	}
	if _, err := strictProductQuery(r); err != nil {
		writeError(w, 400, "INVALID_QUERY", "Query is invalid")
		return
	}
	tx, err := postgres.BeginReadSnapshot(r.Context(), h.pool)
	if err != nil {
		productUnavailable(w)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	out := portfolioDTO{productSource: h.productSource(), Counts: map[string]int64{}}
	err = tx.QueryRow(r.Context(), `SELECT version,total_cents,reserve_bps,principal_owed_cents,reserved_principal_cents,other_commitments_cents FROM servicing.facilities WHERE mode=$1 AND id=$2`, h.cfg.Mode, h.cfg.FacilityID).Scan(&out.Version, &out.Total, &out.ReserveBPS, &out.Funded, &out.Reserved, &out.Other)
	if err != nil {
		productUnavailable(w)
		return
	}
	err = tx.QueryRow(r.Context(), `SELECT COALESCE(sum(principal_owed_cents),0)::bigint,COALESCE(sum(contract_fee_owed_cents::numeric+late_fee_owed_cents),0)::bigint,count(*) FILTER(WHERE cardinality(hold_reasons)>0) FROM servicing.bridges WHERE mode=$1 AND facility_id=$2`, h.cfg.Mode, h.cfg.FacilityID).Scan(&out.Principal, &out.Fees, &out.Held)
	if err != nil {
		productUnavailable(w)
		return
	}
	if err = tx.QueryRow(r.Context(), `SELECT COALESCE(bool_and(history_complete),false) FROM servicing.customers WHERE mode=$1`, h.cfg.Mode).Scan(&out.Complete); err != nil {
		productUnavailable(w)
		return
	}
	var collected int64
	err = tx.QueryRow(r.Context(), `SELECT COALESCE(sum(l.credit_cents::numeric-l.debit_cents),0)::bigint FROM servicing.ledger_lines l JOIN servicing.ledger_transactions t ON t.mode=l.mode AND t.id=l.transaction_id JOIN servicing.bridges b ON b.mode=t.mode AND b.id=t.bridge_id WHERE t.mode=$1 AND b.facility_id=$2 AND t.transaction_kind IN ('COLLECTION_SETTLEMENT','COLLECTION_REVERSAL') AND l.account IN ('CONTRACT_FEE_RECEIVABLE','LATE_FEE_RECEIVABLE')`, h.cfg.Mode, h.cfg.FacilityID).Scan(&collected)
	if err != nil {
		productUnavailable(w)
		return
	}
	if out.Complete {
		out.Collected = &collected
	}
	rows, err := tx.Query(r.Context(), `SELECT lifecycle,count(*) FROM servicing.bridges WHERE mode=$1 AND facility_id=$2 GROUP BY lifecycle`, h.cfg.Mode, h.cfg.FacilityID)
	if err != nil {
		productUnavailable(w)
		return
	}
	for rows.Next() {
		var k string
		var n int64
		if rows.Scan(&k, &n) != nil {
			rows.Close()
			productUnavailable(w)
			return
		}
		out.Counts[k] = n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		productUnavailable(w)
		return
	}
	combined, err := domain.AddMoney(domain.Money(out.Funded), domain.Money(out.Other))
	if err != nil {
		productUnavailable(w)
		return
	}
	if h.evaluator == nil {
		productUnavailable(w)
		return
	}
	description := h.evaluator.DescribeCapital(policy.FacilitySnapshot{Currency: "USD", LedgerComplete: true, FacilityTotalCents: out.Total, ReserveFloorBPS: out.ReserveBPS, FundedPrincipalCents: int64(combined), ReservedPrincipalCents: out.Reserved})
	out.Floor, out.Available, out.Regime = description.ReserveFloorCents, description.AvailableCapitalCents, description.Regime
	out.PolicyID, out.PolicyHash = h.evaluator.PolicyID(), h.evaluator.PolicyHash()
	if !description.Known || tx.Commit(r.Context()) != nil {
		productUnavailable(w)
		return
	}
	writeJSON(w, 200, out)
}

type productCustomer struct {
	ID                string     `json:"customer_id"`
	Name              *string    `json:"display_name"`
	Active            bool       `json:"mapping_active"`
	Available         bool       `json:"accounting_available"`
	Principal         *int64     `json:"principal_owed_cents"`
	Fees              *int64     `json:"unpaid_fee_cents"`
	ReservedPrincipal *int64     `json:"reserved_principal_cents"`
	ReservedFees      *int64     `json:"reserved_fee_cents"`
	Complete          *bool      `json:"history_complete"`
	Hold              *bool      `json:"active_hold"`
	Version           *int64     `json:"version"`
	Captured          *time.Time `json:"captured_at"`
	BridgeID          *string    `json:"active_bridge_id"`
}
type productCustomerPage struct {
	productSource
	Items []productCustomer `json:"items"`
	Next  *string           `json:"next_cursor"`
}

func (h *handler) readProductCustomers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.productActor(w, r, true); !ok {
		return
	}
	detail := r.PathValue("id")
	if detail != "" && !validProductCustomer(detail) {
		writeError(w, 400, "INVALID_REQUEST", "Customer ID is invalid")
		return
	}
	allowed := []string{"cursor"}
	if detail != "" {
		allowed = nil
	}
	q, err := strictProductQuery(r, allowed...)
	if err != nil {
		writeError(w, 400, "INVALID_QUERY", "Query is invalid")
		return
	}
	cur, err := h.productCursor(r, detail, q)
	if err != nil || cur.Created != "" {
		writeError(w, 400, "INVALID_CURSOR", "Cursor is invalid")
		return
	}
	tx, err := postgres.BeginReadSnapshot(r.Context(), h.pool)
	if err != nil {
		productUnavailable(w)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	rows, err := tx.Query(r.Context(), `SELECT r.customer_id,r.is_active,c.id IS NOT NULL,c.principal_owed_cents,c.unpaid_fee_cents,c.reserved_principal_cents,c.reserved_fee_cents,c.history_complete,c.active_hold,c.version,c.updated_at,(SELECT b.id::text FROM servicing.bridges b WHERE b.mode=$1 AND b.facility_id=$2 AND b.customer_id=r.customer_id AND b.lifecycle IN ('RESERVED','FUNDING_PENDING','ACTIVE','COLLECTING','REOPENED_HELD') ORDER BY b.created_at DESC,b.id DESC LIMIT 1) FROM public.customer_roster r LEFT JOIN servicing.customers c ON c.mode=$1 AND c.id=r.customer_id WHERE ($3='' OR r.customer_id=$3) AND r.customer_id>$4 ORDER BY r.customer_id LIMIT $5`, h.cfg.Mode, h.cfg.FacilityID, detail, cur.After, productPageSize+1)
	if err != nil {
		productUnavailable(w)
		return
	}
	out := productCustomerPage{productSource: h.productSource(), Items: []productCustomer{}}
	for rows.Next() {
		var row productCustomer
		if rows.Scan(&row.ID, &row.Active, &row.Available, &row.Principal, &row.Fees, &row.ReservedPrincipal, &row.ReservedFees, &row.Complete, &row.Hold, &row.Version, &row.Captured, &row.BridgeID) != nil {
			rows.Close()
			productUnavailable(w)
			return
		}
		row.Name = demoCustomerLabel(h.cfg.Mode, row.ID)
		out.Items = append(out.Items, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		productUnavailable(w)
		return
	}
	if len(out.Items) > productPageSize {
		out.Items = out.Items[:productPageSize]
		cur.After = out.Items[len(out.Items)-1].ID
		out.Next = encodeProductCursor(cur)
	}
	if tx.Commit(r.Context()) != nil {
		productUnavailable(w)
		return
	}
	if detail != "" {
		if len(out.Items) == 0 {
			writeError(w, 404, "RESOURCE_NOT_FOUND", "Customer not found")
			return
		}
		writeJSON(w, 200, struct {
			productSource
			Customer productCustomer `json:"customer"`
		}{out.productSource, out.Items[0]})
		return
	}
	writeJSON(w, 200, out)
}

type productBridge struct {
	ID                string     `json:"id"`
	Customer          string     `json:"customer_id"`
	Offer             string     `json:"offer_id"`
	Case              *string    `json:"originating_case_id"`
	Evaluation        string     `json:"evaluation_id"`
	Version           int64      `json:"version"`
	Lifecycle         string     `json:"lifecycle"`
	Created           time.Time  `json:"created_at"`
	Funded            *time.Time `json:"funded_at"`
	Due               string     `json:"due_on"`
	Principal         int64      `json:"principal_owed_cents"`
	Fee               int64      `json:"contract_fee_owed_cents"`
	Late              int64      `json:"late_fee_owed_cents"`
	OriginalPrincipal int64      `json:"principal_original_cents"`
	OriginalFee       int64      `json:"contract_fee_original_cents"`
	Term              int        `json:"term_days"`
	Holds             []string   `json:"hold_reasons"`
}
type productBridgePage struct {
	productSource
	Items []productBridge `json:"items"`
	Next  *string         `json:"next_cursor"`
}

func (h *handler) readProductBridges(w http.ResponseWriter, r *http.Request) {
	id, ok := h.productActor(w, r, false)
	if !ok {
		return
	}
	allowed := []string{"cursor"}
	if id.ActorType == "OPERATOR" {
		allowed = append(allowed, "customer_id")
	}
	q, err := strictProductQuery(r, allowed...)
	if err != nil {
		writeError(w, 400, "INVALID_QUERY", "Query is invalid")
		return
	}
	customer := id.CustomerID
	if id.ActorType == "OPERATOR" {
		customer = q.Get("customer_id")
	}
	if !validProductCustomer(customer) {
		writeError(w, 400, "INVALID_QUERY", "Customer filter is required")
		return
	}
	cur, err := h.productCursor(r, customer, q)
	if err != nil {
		writeError(w, 400, "INVALID_CURSOR", "Cursor is invalid")
		return
	}
	var after *time.Time
	if cur.After != "" {
		t, e := time.Parse(time.RFC3339Nano, cur.Created)
		if e != nil || !validUUID(cur.After) {
			writeError(w, 400, "INVALID_CURSOR", "Cursor is invalid")
			return
		}
		after = &t
	}
	tx, err := postgres.BeginReadSnapshot(r.Context(), h.pool)
	if err != nil {
		productUnavailable(w)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	rows, err := tx.Query(r.Context(), `SELECT b.id::text,b.customer_id,b.offer_id::text,b.originating_case_id,b.evaluation_id,b.version,b.lifecycle,b.created_at,b.funded_at,b.due_on::text,b.principal_owed_cents,b.contract_fee_owed_cents,b.late_fee_owed_cents,b.principal_original_cents,b.contract_fee_original_cents,o.term_days,b.hold_reasons FROM servicing.bridges b JOIN servicing.offers o ON o.mode=b.mode AND o.id=b.offer_id WHERE b.mode=$1 AND b.facility_id=$2 AND b.customer_id=$3 AND ($4::timestamptz IS NULL OR (b.created_at,b.id)<($4::timestamptz,$5::uuid)) ORDER BY b.created_at DESC,b.id DESC LIMIT $6`, h.cfg.Mode, h.cfg.FacilityID, customer, after, func() string {
		if cur.After != "" {
			return cur.After
		}
		return "00000000-0000-0000-0000-000000000000"
	}(), productPageSize+1)
	if err != nil {
		productUnavailable(w)
		return
	}
	out := productBridgePage{productSource: h.productSource(), Items: []productBridge{}}
	for rows.Next() {
		var b productBridge
		if rows.Scan(&b.ID, &b.Customer, &b.Offer, &b.Case, &b.Evaluation, &b.Version, &b.Lifecycle, &b.Created, &b.Funded, &b.Due, &b.Principal, &b.Fee, &b.Late, &b.OriginalPrincipal, &b.OriginalFee, &b.Term, &b.Holds) != nil {
			rows.Close()
			productUnavailable(w)
			return
		}
		if b.Holds == nil {
			b.Holds = []string{}
		}
		out.Items = append(out.Items, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		productUnavailable(w)
		return
	}
	if len(out.Items) > productPageSize {
		out.Items = out.Items[:productPageSize]
		last := out.Items[len(out.Items)-1]
		cur.After = last.ID
		cur.Created = last.Created.Format(time.RFC3339Nano)
		out.Next = encodeProductCursor(cur)
	}
	if tx.Commit(r.Context()) != nil {
		productUnavailable(w)
		return
	}
	writeJSON(w, 200, out)
}
