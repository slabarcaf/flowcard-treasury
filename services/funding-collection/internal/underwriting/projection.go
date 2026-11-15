package underwriting

import (
	"errors"
	"math"

	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

// InvoiceEncumbrance identifies one exact invoice commitment from a proven-unsent reservation.
type InvoiceEncumbrance struct {
	InvoiceID       string
	SourceRefSystem string
	StorageSystem   string
	AmountCents     int64
}

// NeverSentReservation is populated only from locked local rows and an operation proven QUEUED.
type NeverSentReservation struct {
	ProvenNeverSent        bool
	OperationState         string
	ReservationState       string
	ReservationID          string
	BridgeID               string
	TransactionID          string
	FacilityPrincipalCents int64
	CustomerPrincipalCents int64
	CustomerFeeCents       int64
	Invoices               []InvoiceEncumbrance
}

// ProjectNeverSentReservation removes only this operation's own locked local
// holds. The policy evaluator then adds the full current shortfall once.
func ProjectNeverSentReservation(input policy.Input, own NeverSentReservation) (policy.Input, error) {
	if !own.ProvenNeverSent || own.OperationState != "QUEUED" || own.ReservationState != "NEVER_SENT" ||
		own.ReservationID == "" || own.BridgeID == "" || own.TransactionID == "" ||
		own.TransactionID != input.Shortfall.TransactionID || own.FacilityPrincipalCents <= 0 ||
		own.FacilityPrincipalCents != own.CustomerPrincipalCents || own.CustomerPrincipalCents <= 0 || own.CustomerFeeCents < 0 ||
		own.CustomerPrincipalCents > math.MaxInt64-own.CustomerFeeCents ||
		!input.Customer.Coverage.BridgeHistory || !input.Customer.Coverage.Reservations || !input.Customer.Coverage.Receivables ||
		input.Customer.Bridges == nil || input.Customer.Reservations == nil || input.Customer.Receivables == nil {
		return policy.Input{}, errors.New("funding_projection_binding_invalid")
	}
	if input.Facility.ReservedPrincipalCents < own.FacilityPrincipalCents {
		return policy.Input{}, errors.New("funding_projection_facility_underflow")
	}
	input.Facility.ReservedPrincipalCents -= own.FacilityPrincipalCents
	input.Customer.Reservations = append([]policy.Reservation(nil), input.Customer.Reservations...)
	input.Customer.Bridges = append([]policy.Bridge(nil), input.Customer.Bridges...)
	input.Customer.Receivables = append([]policy.Receivable(nil), input.Customer.Receivables...)

	reservationIndex := -1
	for index, reservation := range input.Customer.Reservations {
		if reservation.ID != own.ReservationID {
			continue
		}
		if reservationIndex >= 0 || reservation.Status != "ACTIVE" || reservation.BridgeID != nil ||
			reservation.TransactionID != own.TransactionID || reservation.PrincipalCents != own.CustomerPrincipalCents ||
			reservation.FeeCents != own.CustomerFeeCents {
			return policy.Input{}, errors.New("funding_projection_reservation_conflict")
		}
		reservationIndex = index
	}
	if reservationIndex < 0 {
		return policy.Input{}, errors.New("funding_projection_reservation_missing")
	}
	input.Customer.Reservations = append(input.Customer.Reservations[:reservationIndex], input.Customer.Reservations[reservationIndex+1:]...)

	bridgeIndex := -1
	for index, bridge := range input.Customer.Bridges {
		if bridge.ID != own.BridgeID {
			continue
		}
		if bridgeIndex >= 0 || bridge.TransactionID != own.TransactionID || bridge.Status != "OPEN" ||
			bridge.OriginalPrincipalCents != own.CustomerPrincipalCents ||
			bridge.PrincipalOwedCents != 0 || bridge.FeesOwedCents != 0 || bridge.ClosedOn != nil || bridge.DefaultedOn != nil {
			return policy.Input{}, errors.New("funding_projection_bridge_conflict")
		}
		bridgeIndex = index
	}
	if bridgeIndex < 0 {
		return policy.Input{}, errors.New("funding_projection_placeholder_missing")
	}
	input.Customer.Bridges = append(input.Customer.Bridges[:bridgeIndex], input.Customer.Bridges[bridgeIndex+1:]...)

	var released int64
	for _, encumbrance := range own.Invoices {
		if encumbrance.InvoiceID == "" || encumbrance.SourceRefSystem == "" || encumbrance.StorageSystem == "" ||
			encumbrance.AmountCents <= 0 {
			return policy.Input{}, errors.New("funding_projection_invoice_invalid")
		}
		match := -1
		for index, invoice := range input.Customer.Receivables {
			if invoice.InvoiceID != encumbrance.InvoiceID || invoice.SourceRef.System != encumbrance.SourceRefSystem {
				continue
			}
			if match >= 0 {
				return policy.Input{}, errors.New("funding_projection_invoice_ambiguous")
			}
			match = index
		}
		if match < 0 || input.Customer.Receivables[match].AllocatedCents < encumbrance.AmountCents {
			return policy.Input{}, errors.New("funding_projection_invoice_underflow")
		}
		input.Customer.Receivables[match].AllocatedCents -= encumbrance.AmountCents
		if released > math.MaxInt64-encumbrance.AmountCents {
			return policy.Input{}, errors.New("funding_projection_amount_overflow")
		}
		released += encumbrance.AmountCents
	}
	if released != own.CustomerPrincipalCents+own.CustomerFeeCents {
		return policy.Input{}, errors.New("funding_projection_invoice_total_mismatch")
	}
	return input, nil
}
