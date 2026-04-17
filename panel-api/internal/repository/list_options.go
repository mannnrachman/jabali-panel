package repository

import (
	"strings"

	"gorm.io/gorm"
)

// ListOptions is the common request shape for paginated list queries that
// also want free-text search and single-column sort. All repositories'
// List* methods take this struct; the zero value (offset=0, limit=0, no
// search, no sort) is valid and produces the historical "give me
// everything in insertion order" behaviour — callers that want a default
// page size should fill in Limit themselves.
//
// Security: Sort is whitelist-matched against a per-repo allowlist before
// it ever hits the SQL. An unknown Sort value silently falls back to the
// repo's default ordering (usually created_at DESC). Search is always
// parameterised — the only thing the caller controls is the LIKE pattern.
type ListOptions struct {
	Offset int
	Limit  int
	Search string // free-text, matched via ILIKE across the repo's search columns
	Sort   string // column name (will be whitelist-validated per repo)
	Order  string // "asc" | "desc" — anything else coerces to "desc"
}

// ListCols tells applyListOptions which columns are searchable (free-text
// LIKE) and which are sortable (ORDER BY). Both lists are whitelists —
// empty string keys are impossible so SQL injection via Sort/Search names
// is structurally prevented.
type ListCols struct {
	Search      []string // columns included in the ILIKE OR-chain
	Sort        []string // columns accepted as the sort key
	DefaultSort string   // column used when Sort is empty or not in allowlist
}

// applyListOptions layers search/sort/pagination onto a base *gorm.DB
// scope. It is used by every repo's List method to keep behaviour (and
// whitelist enforcement) identical across resources.
//
// Intentional choices:
//   - Uses LIKE not ILIKE because MariaDB's default collations are
//     case-insensitive already; ILIKE would be Postgres-only.
//   - Sort direction defaults to DESC on the default column; explicit
//     Sort+Order can flip it.
//   - Empty Limit leaves the query unbounded (callers should clamp).
func applyListOptions(base *gorm.DB, opts ListOptions, cols ListCols) *gorm.DB {
	q := base

	if s := strings.TrimSpace(opts.Search); s != "" && len(cols.Search) > 0 {
		pattern := "%" + s + "%"
		// Build "col1 LIKE ? OR col2 LIKE ? OR …" with a single args slice.
		clauses := make([]string, 0, len(cols.Search))
		args := make([]any, 0, len(cols.Search))
		for _, c := range cols.Search {
			clauses = append(clauses, c+" LIKE ?")
			args = append(args, pattern)
		}
		q = q.Where(strings.Join(clauses, " OR "), args...)
	}

	sort := pickSort(opts.Sort, cols.Sort, cols.DefaultSort)
	order := "DESC"
	if strings.EqualFold(opts.Order, "asc") {
		order = "ASC"
	}
	if sort != "" {
		q = q.Order(sort + " " + order)
	}

	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	return q
}

// pickSort returns the requested column if it's in the allowlist, or the
// default column otherwise. Empty string means "don't order" — callers
// that pass an empty DefaultSort get that.
func pickSort(requested string, allowed []string, fallback string) string {
	r := strings.ToLower(strings.TrimSpace(requested))
	if r == "" {
		return fallback
	}
	for _, c := range allowed {
		if strings.EqualFold(c, r) {
			return c
		}
	}
	return fallback
}
