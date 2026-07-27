//go:build !linux

package slogs

import "os"

// chown is a no-op on non-linux platforms.
func chown(_ string, _ os.FileInfo) error {
	return nil
}
