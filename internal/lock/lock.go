// Package lock serializes pathctl runs that read or change the configuration
// and the override directory, so that one run never acts on a configuration
// another run has already replaced.
package lock

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// Acquire blocks until it holds an exclusive lock on path, creating the file
// and its directory if needed. The returned function releases it.
func Acquire(path string) (release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	// Closing the file releases the lock.
	return func() { f.Close() }, nil
}
