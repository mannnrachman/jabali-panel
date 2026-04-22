package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// ManagedIPRepository covers the managed_ips pool table introduced in
// migration 000057. Domain ↔ IP binding is enforced by the FK on
// domains.listen_ipv*_id (migration 000058) — CountDomainsUsingIP is the
// helper handlers call before DELETE so the API can return 409 with the
// affected-domains list before MariaDB raises the FK error.
type ManagedIPRepository interface {
	Create(ctx context.Context, ip *models.ManagedIP) error
	Update(ctx context.Context, ip *models.ManagedIP) error
	Delete(ctx context.Context, id uint64) error

	FindByID(ctx context.Context, id uint64) (*models.ManagedIP, error)
	FindByAddress(ctx context.Context, address string) (*models.ManagedIP, error)

	ListAll(ctx context.Context) ([]models.ManagedIP, error)
	// FindUnbound returns rows where is_bound=false. Used by the agent
	// reconcile-on-start loop (Step 4) to know which addresses still
	// need an `ip addr add` call after a host reboot.
	FindUnbound(ctx context.Context) ([]models.ManagedIP, error)

	// CountDomainsUsingIP returns the number of domains referencing this
	// managed_ip via either listen_ipv4_id or listen_ipv6_id. Used by
	// DELETE handlers to short-circuit with a 409 + reason instead of
	// letting MariaDB raise an FK violation.
	CountDomainsUsingIP(ctx context.Context, id uint64) (int64, error)

	// FindDefaultByFamily returns the row with is_default=TRUE for the
	// given family ("ipv4"/"ipv6"), or ErrNotFound. Used by the API to
	// resolve "use server default" when a domain's listen_ipv*_id is NULL.
	FindDefaultByFamily(ctx context.Context, family string) (*models.ManagedIP, error)

	// EnsureDefault creates the is_default row for the given family if
	// none exists. Idempotent: no-op if a default for the family already
	// exists, no-op if address is empty. Used by panel-api's first-boot
	// seed (serve.go) to materialise the default IP row *after*
	// server_settings has been populated — migration 000057 can't do it
	// because it runs before install.sh gives panel-api the IP value.
	EnsureDefault(ctx context.Context, address, family string) error
}

type managedIPRepo struct{ db *gorm.DB }

func NewManagedIPRepository(db *gorm.DB) ManagedIPRepository {
	return &managedIPRepo{db: db}
}

func (r *managedIPRepo) Create(ctx context.Context, ip *models.ManagedIP) error {
	if err := r.db.WithContext(ctx).Create(ip).Error; err != nil {
		if isDuplicateKey(err) {
			return ErrConflict
		}
		return err
	}
	return nil
}

func (r *managedIPRepo) Update(ctx context.Context, ip *models.ManagedIP) error {
	return r.db.WithContext(ctx).Save(ip).Error
}

func (r *managedIPRepo) Delete(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&models.ManagedIP{}).Error
}

func (r *managedIPRepo) FindByID(ctx context.Context, id uint64) (*models.ManagedIP, error) {
	var row models.ManagedIP
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (r *managedIPRepo) FindByAddress(ctx context.Context, address string) (*models.ManagedIP, error) {
	var row models.ManagedIP
	if err := r.db.WithContext(ctx).Where("address = ?", address).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (r *managedIPRepo) ListAll(ctx context.Context) ([]models.ManagedIP, error) {
	var rows []models.ManagedIP
	// Stable order: family first, then is_default desc (default rows on
	// top), then label, then id. The pool is small (R8 caps at 100) so
	// no pagination is needed.
	err := r.db.WithContext(ctx).
		Order("family asc, is_default desc, label asc, id asc").
		Find(&rows).Error
	return rows, err
}

func (r *managedIPRepo) FindUnbound(ctx context.Context) ([]models.ManagedIP, error) {
	var rows []models.ManagedIP
	err := r.db.WithContext(ctx).
		Where("is_bound = ?", false).
		Order("id asc").
		Find(&rows).Error
	return rows, err
}

func (r *managedIPRepo) CountDomainsUsingIP(ctx context.Context, id uint64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Table("domains").
		Where("listen_ipv4_id = ? OR listen_ipv6_id = ?", id, id).
		Count(&count).Error
	return count, err
}

func (r *managedIPRepo) FindDefaultByFamily(ctx context.Context, family string) (*models.ManagedIP, error) {
	var row models.ManagedIP
	err := r.db.WithContext(ctx).
		Where("family = ? AND is_default = ?", family, true).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (r *managedIPRepo) EnsureDefault(ctx context.Context, address, family string) error {
	if address == "" {
		return nil
	}
	if family != "ipv4" && family != "ipv6" {
		return errors.New("ensure default: family must be ipv4 or ipv6")
	}
	// Existing is_default row for this family? Nothing to do.
	if _, err := r.FindDefaultByFamily(ctx, family); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	// Someone may have already created a row for this address with
	// is_default=false (e.g. pre-bound externally via the admin API
	// before first boot completed). Flip it to default rather than
	// failing on the unique-address constraint.
	if existing, err := r.FindByAddress(ctx, address); err == nil {
		if !existing.IsDefault {
			existing.IsDefault = true
			if existing.Label == "" {
				existing.Label = "server primary (" + shortFamily(family) + ")"
			}
			return r.Update(ctx, existing)
		}
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	row := &models.ManagedIP{
		Address:          address,
		Family:           family,
		Label:            "server primary (" + shortFamily(family) + ")",
		IsDefault:        true,
		IsBound:          false,
		IsUserSelectable: false,
	}
	if err := r.Create(ctx, row); err != nil {
		// Race with a concurrent seed — someone beat us to the insert.
		// Treat as success; the default row exists now.
		if errors.Is(err, ErrConflict) {
			return nil
		}
		return err
	}
	return nil
}

func shortFamily(f string) string {
	if f == "ipv4" {
		return "v4"
	}
	return "v6"
}

// isDuplicateKey returns true when err is a MariaDB duplicate-entry
// (1062) wrapped through gorm. We can't rely on gorm.ErrDuplicatedKey
// because the version pinned in this repo doesn't expose it for raw
// driver errors — and string-matching the message is what other repos
// in this codebase already do. Match either the typed driver error or
// the legacy string fallback.
func isDuplicateKey(err error) bool {
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		return true
	}
	return strings.Contains(err.Error(), "Error 1062") ||
		strings.Contains(err.Error(), "Duplicate entry")
}
