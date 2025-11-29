package storage

import "errors"

var (
	// ErrStorageClosed is returned when operations are performed on a closed storage.
	ErrStorageClosed = errors.New("storage is closed")

	// ErrStorageUnavailable is returned when the storage backend is unreachable.
	ErrStorageUnavailable = errors.New("storage is unavailable")

	// ErrKeyNotFound is returned when a key does not exist in storage.
	ErrKeyNotFound = errors.New("key not found")

	// ErrInvalidState is returned when the stored state is invalid or corrupted.
	ErrInvalidState = errors.New("invalid bucket state")
)
