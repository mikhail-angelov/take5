//go:build windows

package install

import "golang.org/x/sys/windows/registry"

// registerWindows points Chrome at the manifest through the registry key it actually reads
// on this platform: HKCU\Software\<Vendor>\NativeMessagingHosts\<name>, default value the
// manifest's path (SPIKE.md §1 consequences — "install resolves absolute paths ... and
// bakes them into a generated launcher", the Windows-specific half of that).
func registerWindows(browser, manifestPath string) error {
	vendor := "Google\\Chrome"
	if browser == "Chromium" {
		vendor = "Chromium"
	}
	keyPath := `Software\` + vendor + `\NativeMessagingHosts\` + HostName
	k, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue("", manifestPath)
}
