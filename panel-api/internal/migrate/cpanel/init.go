package cpanel

import (
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func init() {
	migrate.Register(models.MigrationSourceCpanel, func() migrate.Discoverer {
		return New()
	})
	// whm_pkgacct is the WHM-side flow: a connected admin session
	// runs `whmapi1 listaccts` and per-account pkgacct. The wire
	// shape + Discoverer logic are the same as cpanel-single, just
	// with PrincipalAdmin instead of PrincipalUser (autodetected in
	// Connect). Register the SAME Discoverer factory under the WHM
	// kind so /admin/migrations/:id/discover-accounts finds it. ADR-
	// 0095 decision 1 + decision 3.
	migrate.Register(models.MigrationSourceWHMpkgacct, func() migrate.Discoverer {
		return New()
	})
}
