package repository

import (
	"context"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// TetragonPolicyStateRepository tracks enabled/disabled flags for the
// per-jabali Tetragon TracingPolicies under /etc/tetragon/tetragon.tp.d/.
// Filesystem (.yaml vs .yaml.disabled rename) is the actual control;
// this table holds the mirror + audit (updated_by).
type TetragonPolicyStateRepository interface {
	List(ctx context.Context) ([]models.TetragonPolicyState, error)
	Get(ctx context.Context, name string) (*models.TetragonPolicyState, error)
	Upsert(ctx context.Context, state *models.TetragonPolicyState) error
}

type tetragonPolicyStateRepo struct{ db *gorm.DB }

// NewTetragonPolicyStateRepository returns a GORM-backed policy state repo.
func NewTetragonPolicyStateRepository(db *gorm.DB) TetragonPolicyStateRepository {
	return &tetragonPolicyStateRepo{db: db}
}

func (r *tetragonPolicyStateRepo) List(ctx context.Context) ([]models.TetragonPolicyState, error) {
	var out []models.TetragonPolicyState
	if err := r.db.WithContext(ctx).
		Order("policy_name ASC").
		Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

func (r *tetragonPolicyStateRepo) Get(ctx context.Context, name string) (*models.TetragonPolicyState, error) {
	var out models.TetragonPolicyState
	if err := r.db.WithContext(ctx).
		Where("policy_name = ?", name).
		First(&out).Error; err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

// Upsert inserts or updates by policy_name PK.
func (r *tetragonPolicyStateRepo) Upsert(ctx context.Context, state *models.TetragonPolicyState) error {
	res := r.db.WithContext(ctx).
		Where("policy_name = ?", state.PolicyName).
		Assign(map[string]any{
			"enabled":    state.Enabled,
			"updated_by": state.UpdatedBy,
		}).
		FirstOrCreate(state)
	if res.Error != nil {
		return translate(res.Error)
	}
	return nil
}
