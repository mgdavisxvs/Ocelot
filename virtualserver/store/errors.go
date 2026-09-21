package store

import "errors"

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("resource not found")

// ErrConflict is returned when a unique constraint would be violated.
var ErrConflict = errors.New("resource already exists")

// ErrAllocationConflict is returned when concurrent allocation cannot be satisfied.
var ErrAllocationConflict = errors.New("allocation conflict: resources already claimed")
