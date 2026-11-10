//go:build !windows

package skl

import "errors"

// createJunction is only available on Windows; elsewhere symlinks are always used.
func createJunction(link, target string) error {
	return errors.New("directory junctions are only supported on Windows")
}
