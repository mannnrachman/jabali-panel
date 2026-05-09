package cpanel

import (
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func init() {
	migrate.Register(models.MigrationSourceCpanel, func() migrate.Discoverer {
		return New()
	})
}
