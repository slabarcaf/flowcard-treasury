package webhooks

import (
	"context"
	"log"
	"time"
)

// Reconciler runs periodic background polling and status reconciliation.
type Reconciler struct {
	processor    *Processor
	pollInterval time.Duration
}

// NewReconciler constructs a periodic Webhook Reconciler.
func NewReconciler(processor *Processor, pollInterval time.Duration) *Reconciler {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	return &Reconciler{
		processor:    processor,
		pollInterval: pollInterval,
	}
}

// Run executes the reconciler loop until ctx is canceled.
func (r *Reconciler) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	// Initial drain on startup
	if _, err := r.processor.ProcessPending(ctx); err != nil && !errorsIsCanceled(err) {
		log.Printf("webhook reconciler initial run error: %v", err)
	}
	if _, err := r.processor.ReconcileAccounts(ctx); err != nil && !errorsIsCanceled(err) {
		log.Printf("webhook account reconciler initial run error: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := r.processor.ProcessPending(ctx); err != nil && !errorsIsCanceled(err) {
				log.Printf("webhook reconciler batch error: %v", err)
			}
			if _, err := r.processor.ReconcileAccounts(ctx); err != nil && !errorsIsCanceled(err) {
				log.Printf("webhook account reconciler batch error: %v", err)
			}
		}
	}
}

func errorsIsCanceled(err error) bool {
	if err == nil {
		return false
	}
	return err == context.Canceled || err == context.DeadlineExceeded
}
