package phases

import (
	"context"
	"sync"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// Phase defines the reconciliation contract for an M6.5 email feature.
// Each phase owns convergence of its domain/mailbox state to Stalwart.
type Phase interface {
	Name() string
	ReconcileDomain(ctx context.Context, domain *models.Domain, config map[string]interface{}) error
	ReconcileMailbox(ctx context.Context, mailbox *models.Mailbox, domain *models.Domain, config map[string]interface{}) error
}

var (
	phasesMu sync.RWMutex
	phases   []Phase
)

// RegisterPhase registers a feature phase for reconciliation.
// Called during each feature's init().
func RegisterPhase(p Phase) {
	phasesMu.Lock()
	defer phasesMu.Unlock()
	phases = append(phases, p)
}

// Phases returns a copy of all registered phases.
func Phases() []Phase {
	phasesMu.RLock()
	defer phasesMu.RUnlock()
	result := make([]Phase, len(phases))
	copy(result, phases)
	return result
}

// ReconcileDomainAll runs all registered phases' domain reconciliation.
func ReconcileDomainAll(ctx context.Context, domain *models.Domain, config map[string]interface{}) error {
	for _, p := range Phases() {
		if err := p.ReconcileDomain(ctx, domain, config); err != nil {
			return err
		}
	}
	return nil
}

// ReconcileMailboxAll runs all registered phases' mailbox reconciliation.
func ReconcileMailboxAll(ctx context.Context, mailbox *models.Mailbox, domain *models.Domain, config map[string]interface{}) error {
	for _, p := range Phases() {
		if err := p.ReconcileMailbox(ctx, mailbox, domain, config); err != nil {
			return err
		}
	}
	return nil
}
