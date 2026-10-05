//go:build !unix

package obsdconf

// checkSecrecy is a no-op on systems without Unix file permissions.
func checkSecrecy(path string) error {
	return nil
}
