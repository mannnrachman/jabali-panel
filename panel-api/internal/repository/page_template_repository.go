package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// PageTemplateRepository wraps the page_templates table. Rows are
// addressed by key; the canonical set lives in
// models.AllPageTemplateKeys. Admin API validates incoming key
// against that set.
type PageTemplateRepository interface {
	List(ctx context.Context) ([]models.PageTemplate, error)
	Get(ctx context.Context, key string) (*models.PageTemplate, error)
	Upsert(ctx context.Context, key, content string) error
	EnsureDefaults(ctx context.Context) (seeded int, err error)
}

type pageTemplateRepo struct{ db *gorm.DB }

func NewPageTemplateRepository(db *gorm.DB) PageTemplateRepository {
	return &pageTemplateRepo{db: db}
}

func (r *pageTemplateRepo) List(ctx context.Context) ([]models.PageTemplate, error) {
	var rows []models.PageTemplate
	if err := r.db.WithContext(ctx).Order("`key` ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *pageTemplateRepo) Get(ctx context.Context, key string) (*models.PageTemplate, error) {
	var row models.PageTemplate
	if err := r.db.WithContext(ctx).Where("`key` = ?", key).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (r *pageTemplateRepo) Upsert(ctx context.Context, key, content string) error {
	now := time.Now().UTC()
	row := models.PageTemplate{Key: key, Content: content, UpdatedAt: now}

	// Try update first; if zero rows affected, insert.
	res := r.db.WithContext(ctx).
		Model(&models.PageTemplate{}).
		Where("`key` = ?", key).
		Updates(map[string]any{"content": content, "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&row).Error
}

// EnsureDefaults seeds any missing template rows with the baked-in
// default body. Idempotent — only inserts keys that don't already
// exist. Returns the count of newly-inserted rows.
func (r *pageTemplateRepo) EnsureDefaults(ctx context.Context) (int, error) {
	var existing []string
	if err := r.db.WithContext(ctx).
		Model(&models.PageTemplate{}).
		Pluck("key", &existing).Error; err != nil {
		return 0, err
	}
	have := make(map[string]struct{}, len(existing))
	for _, k := range existing {
		have[k] = struct{}{}
	}

	now := time.Now().UTC()
	seeded := 0
	for _, key := range models.AllPageTemplateKeys {
		if _, ok := have[key]; ok {
			continue
		}
		row := models.PageTemplate{Key: key, Content: DefaultPageTemplateBody(key), UpdatedAt: now}
		if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
			return seeded, err
		}
		seeded++
	}
	return seeded, nil
}

// DefaultPageTemplateBody returns the built-in default body for a
// known template key. Used by EnsureDefaults on first boot AND by
// the "Reset to default" UI action. Kept here rather than in the
// agent so operators see the same defaults regardless of which host
// serves the page.
//
// domain_default_index renders Go template placeholders {{.Domain}},
// {{.Username}}, {{.DocRoot}} expanded by the agent at write-time.
// Error pages are static HTML.
func DefaultPageTemplateBody(key string) string {
	switch key {
	case models.PageTemplateDomainDefaultIndex:
		return defaultIndexTemplate
	case models.PageTemplateError404:
		return defaultError404
	case models.PageTemplateError403:
		return defaultError403
	case models.PageTemplateError500:
		return defaultError500
	}
	return ""
}

const defaultIndexTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>{{.Domain}}</title>
  <style>
    body { font-family: system-ui, -apple-system, Segoe UI, Roboto, sans-serif; max-width: 640px; margin: 4rem auto; padding: 0 1.25rem; color: #222; line-height: 1.5; }
    h1 { color: #1976d2; margin-bottom: 0.25em; }
    .muted { color: #666; margin-top: 0; }
    code { background: #f5f5f5; padding: 0.15rem 0.4rem; border-radius: 4px; font-size: 0.92em; font-family: ui-monospace, Menlo, Consolas, monospace; }
    hr { border: none; border-top: 1px solid #eee; margin: 2rem 0; }
    small { color: #888; }
  </style>
</head>
<body>
  <h1>{{.Domain}}</h1>
  <p class="muted">This domain is hosted by Jabali Panel. The site is provisioned and waiting for your content.</p>
  <hr>
  <p>Upload your files to the document root:</p>
  <p><code>{{.DocRoot}}</code></p>
  <p><small>Logged in as <code>{{.Username}}</code>.</small></p>
</body>
</html>
`

const defaultError404 = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>404 — Not Found</title>
  <style>body{font-family:system-ui,sans-serif;max-width:640px;margin:4rem auto;padding:0 1.25rem;color:#222;text-align:center;}h1{font-size:3rem;color:#1976d2;margin-bottom:.25em;}p{color:#666;}</style>
</head>
<body>
  <h1>404</h1>
  <p>The page you requested could not be found.</p>
</body>
</html>
`

const defaultError403 = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>403 — Forbidden</title>
  <style>body{font-family:system-ui,sans-serif;max-width:640px;margin:4rem auto;padding:0 1.25rem;color:#222;text-align:center;}h1{font-size:3rem;color:#b71c1c;margin-bottom:.25em;}p{color:#666;}</style>
</head>
<body>
  <h1>403</h1>
  <p>You do not have permission to access this resource.</p>
</body>
</html>
`

const defaultError500 = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>500 — Server Error</title>
  <style>body{font-family:system-ui,sans-serif;max-width:640px;margin:4rem auto;padding:0 1.25rem;color:#222;text-align:center;}h1{font-size:3rem;color:#b71c1c;margin-bottom:.25em;}p{color:#666;}</style>
</head>
<body>
  <h1>500</h1>
  <p>An internal error has occurred. Please try again later.</p>
</body>
</html>
`
