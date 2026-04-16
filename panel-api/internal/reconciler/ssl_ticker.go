package reconciler

import (
	"context"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// StartSSLTicker runs a background loop that periodically checks for SSL
// certificates due for renewal and marks them for renewal.
// It checks daily and looks for certificates expiring within 30 days.
// This is separate from the main reconciler loop to decouple SSL renewal
// scheduling from domain reconciliation.
//
// The ticker runs in its own goroutine and stops when ctx is cancelled.
func StartSSLTicker(ctx context.Context, sslRepo repository.SSLCertificateRepository, log interface {
	Info(string, ...interface{})
	Error(string, ...interface{})
}) {
	if sslRepo == nil {
		return // SSL feature not wired — skip
	}

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	log.Info("ssl_ticker starting", "interval", 24*time.Hour)

	// Run once at startup to catch any certificates that need renewal
	checkAndMarkDueForRenewal(ctx, sslRepo, log)

	for {
		select {
		case <-ctx.Done():
			log.Info("ssl_ticker stopping")
			return
		case <-ticker.C:
			checkAndMarkDueForRenewal(ctx, sslRepo, log)
		}
	}
}

// checkAndMarkDueForRenewal queries for SSL certificates expiring within
// 30 days and marks them with status "renewing" if they're currently "issued".
// This allows the main reconciler to pick them up and handle renewal.
func checkAndMarkDueForRenewal(ctx context.Context, sslRepo repository.SSLCertificateRepository, log interface {
	Info(string, ...interface{})
	Error(string, ...interface{})
}) {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Look for certificates expiring within 30 days
	certs, err := sslRepo.ListDueForRenewal(checkCtx, 30*24*time.Hour)
	if err != nil {
		log.Error("ssl_ticker: list due for renewal failed", "err", err)
		return
	}

	for i := range certs {
		cert := &certs[i]

		// Only renew certificates that are currently in "issued" state
		if cert.Status != "issued" {
			continue
		}

		// Mark for renewal
		updateCtx, updateCancel := context.WithTimeout(ctx, 5*time.Second)
		err := sslRepo.UpdateStatus(updateCtx, cert.ID, "renewing", nil)
		updateCancel()
		if err != nil {
			log.Error("ssl_ticker: mark for renewal failed",
				"cert_id", cert.ID,
				"domain_id", cert.DomainID,
				"err", err)
		} else {
			log.Info("ssl_ticker: marked for renewal",
				"cert_id", cert.ID,
				"domain_id", cert.DomainID,
				"expires_at", cert.ExpiresAt)
		}
	}
}
