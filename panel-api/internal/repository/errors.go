// Package repository wraps the panel's MariaDB access behind small, testable
// interfaces. Each repository has one responsibility (users, refresh tokens,
// etc.). Service layers depend on these interfaces, not on *gorm.DB directly,
// so tests can swap in mocks.
package repository

import "errors"

// ErrNotFound is returned when a lookup by a unique key (email, id, token
// hash) finds no row. Wraps into gorm.ErrRecordNotFound for callers that
// check repository errors, without leaking the GORM type.
var ErrNotFound = errors.New("repository: not found")

// ErrConflict is returned when a write violates a unique constraint
// (duplicate email, duplicate token_hash). Callers translate this into
// HTTP 409 at the handler boundary.
var ErrConflict = errors.New("repository: conflict")
