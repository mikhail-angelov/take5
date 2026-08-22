//go:build !windows

package install

// registerWindows is only meaningful on Windows, where Chrome finds a native-messaging
// host manifest through a registry key rather than a fixed directory. Elsewhere the
// manifest's directory alone is enough.
func registerWindows(_, _ string) error {
	return nil
}
