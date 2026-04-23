// Package phases provides a plugin-based reconciliation system for M6 email features.
//
// Each feature (forwarders, autoresponders, catch-all, disclaimer, shared folders, logs)
// registers itself as a Phase during init(). The main reconciler loop runs all registered
// phases in sequence, enabling parallel Wave development without file collisions on
// reconciler.go or router.go.
//
// Phase Interface
//
// Each phase implements:
//   - Name(): string - Unique phase identifier for logging/debugging
//   - ReconcileDomain(ctx, domain, config) error - Converge domain state
//   - ReconcileMailbox(ctx, mailbox, domain, config) error - Converge mailbox state
//
// This pattern extends the mailbox-only reconciliation from M6 (ADR-0041) to encompass
// all email feature state, maintaining jabali-as-truth across the Stalwart integration layer.
//
// Registration
//
// Each feature creates a file under this package:
//   - m65_forwarders.go: RegisterPhase(&forwardersPhase{})
//   - m65_autoresponders.go: RegisterPhase(&autoresppondersPhase{})
//   - m65_catchall.go: RegisterPhase(&catchallPhase{})
//   - etc.
//
// The registrar is called during each feature's init(), guaranteeing registration
// before the main reconciler loop starts.
//
// ADR-0051 documents the jabali-as-truth pattern and Stalwart integration for all six features.
package phases
