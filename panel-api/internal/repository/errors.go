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

// ErrLocked is returned when a SELECT ... FOR UPDATE NOWAIT fails to acquire
// the lock immediately (another transaction holds it). This indicates a concurrent
// attempt to consume the same magic link token.
var ErrLocked = errors.New("repository: locked")

// ErrAlreadyUsed is returned when a token has already been consumed (UsedAt is not nil).
// This prevents single-use enforcement from being bypassed.
var ErrAlreadyUsed = errors.New("repository: already used")
