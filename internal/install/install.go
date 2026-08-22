// Package install writes the native-messaging host manifest that lets Chrome spawn the
// take5 binary, and diagnoses the pieces install depends on (SPIKE.md §1
// consequences). Because the binary has no interpreter, install registers it directly —
// no launcher shim, unlike the Node host this replaces (docs/plans/go-port.md).
package install

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"

	"take5/internal/render"
	"take5/internal/transcribe"
	"take5/internal/voiceover"
)

// HostName is the name of the native messaging host.
const HostName = "com.demo_recorder.host"

// ExtensionID replicates Chrome's derivation of an extension id from its manifest key:
// sha256 of the DER SPKI public key, first 16 bytes, hex digits mapped onto a-p. Verified
// against a real Chrome load in SPIKE.md §1.3. base64Key is the extension manifest's "key"
// field.
func ExtensionID(base64Key string) (string, error) {
	der, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return "", fmt.Errorf("key is not valid base64: %w", err)
	}
	sum := sha256.Sum256(der)
	id := make([]byte, 32)
	for i := range 16 {
		id[i*2] = 'a' + (sum[i] >> 4)
		id[i*2+1] = 'a' + (sum[i] & 0x0f)
	}
	return string(id), nil
}

// ReadExtensionKey pulls the "key" field out of extension/manifest.json.
func ReadExtensionKey(manifestPath string) (string, error) {
	buf, err := os.ReadFile(manifestPath) // #nosec G304
	if err != nil {
		return "", fmt.Errorf("could not read %s: %w", manifestPath, err)
	}
	var m struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(buf, &m); err != nil {
		return "", fmt.Errorf("could not parse %s: %w", manifestPath, err)
	}
	if m.Key == "" {
		return "", fmt.Errorf("%s has no \"key\" field; pass -key explicitly, or add one", manifestPath)
	}
	return m.Key, nil
}

// Manifest is a native messaging host manifest.
type Manifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

// BuildManifest creates a manifest for the given executable path and extension ID.
func BuildManifest(execPath, extensionID string) Manifest {
	return Manifest{
		Name:           HostName,
		Description:    "take5 native messaging host",
		Path:           execPath,
		Type:           "stdio",
		AllowedOrigins: []string{fmt.Sprintf("chrome-extension://%s/", extensionID)},
	}
}

// browserManifestDirs lists where Chrome and Chromium look for native-messaging host
// manifests on this OS. A directory is only used if its browser's parent profile
// directory exists, i.e. that browser is actually installed on this machine.
func browserManifestDirs() (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return map[string]string{
			"Google Chrome": filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "NativeMessagingHosts"),
			"Chromium":      filepath.Join(home, "Library", "Application Support", "Chromium", "NativeMessagingHosts"),
		}, nil
	case "linux":
		return map[string]string{
			"Google Chrome": filepath.Join(home, ".config", "google-chrome", "NativeMessagingHosts"),
			"Chromium":      filepath.Join(home, ".config", "chromium", "NativeMessagingHosts"),
		}, nil
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		// Windows has no fixed manifest directory Chrome scans; a registry key points at
		// wherever install puts it. See install_windows.go.
		return map[string]string{
			"Google Chrome": filepath.Join(base, "take5", "NativeMessagingHosts"),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// WrittenManifest describes where a manifest was written.
type WrittenManifest struct {
	Browser string
	Path    string
}

// Install writes the host manifest for every detected browser and registers it (on Windows,
// in the registry; elsewhere Chrome finds the file by its fixed directory alone).
func Install(execPath, base64Key string) ([]WrittenManifest, error) {
	id, err := ExtensionID(base64Key)
	if err != nil {
		return nil, err
	}
	manifest := BuildManifest(execPath, id)
	buf, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	buf = append(buf, '\n')

	dirs, err := browserManifestDirs()
	if err != nil {
		return nil, err
	}

	var written []WrittenManifest
	for browser, dir := range dirs {
		if runtime.GOOS != "windows" {
			parent := filepath.Dir(dir)
			if _, err := os.Stat(parent); err != nil {
				continue // that browser is not installed on this machine
			}
		}
		if err := os.MkdirAll(dir, 0o750); err != nil { // #nosec G301
			return written, fmt.Errorf("create directory: %w", err)
		}
		path := filepath.Join(dir, HostName+".json")
		if err := os.WriteFile(path, buf, 0o600); err != nil { // #nosec G306
			return written, fmt.Errorf("write file: %w", err)
		}
		if err := registerWindows(browser, path); err != nil {
			return written, err
		}
		written = append(written, WrittenManifest{Browser: browser, Path: path})
	}
	if len(written) == 0 {
		return nil, errors.New("no supported Chrome or Chromium profile directory found on this machine")
	}
	return written, nil
}

// Report is the diagnostic output of the Doctor check.
type Report struct {
	ExecPath   string
	ExecExists bool

	FfmpegPath  string
	FfmpegFound bool

	FfprobePath  string
	FfprobeFound bool

	// WhisperFound/WhisperModelFound: like ffmpeg above, these matter only when the voice
	// annotation feature is opted into (docs/plans/2026-08-19-voice-annotations.md Task 3) —
	// their absence is not a doctor failure for the base product, just a note for that
	// feature's own prerequisite.
	WhisperPath  string
	WhisperFound bool

	WhisperModelPath  string
	WhisperModelFound bool

	// EdgeTTSFound: same opt-in-feature caveat as WhisperFound/WhisperModelFound above —
	// edge-tts is the `voice` subcommand's TTS step, not a base-product dependency.
	EdgeTTSPath  string
	EdgeTTSFound bool

	Manifests []ManifestCheck
}

// ManifestCheck is diagnostic information about a manifest file.
type ManifestCheck struct {
	Browser        string
	Path           string
	Exists         bool
	HostPath       string
	HostExecutable bool
	Issue          string
}

// Doctor re-checks everything install depends on, independently of whatever install last
// wrote — the diagnosis SPIKE.md §1.5 says a bad PATH makes otherwise invisible.
func Doctor(base64Key string) Report {
	report := Report{}

	if execPath, err := os.Executable(); err == nil {
		report.ExecPath = execPath
		if info, err := os.Stat(execPath); err == nil && !info.IsDir() {
			report.ExecExists = true
		}
	}

	// render.FfmpegBin/FfprobeBin already probe past this process's own PATH (SPIKE.md §5);
	// exec.LookPath here only confirms the result is actually an executable file, and still
	// works when that result is already an absolute path.
	if p, err := exec.LookPath(render.FfmpegBin()); err == nil {
		report.FfmpegPath = p
		report.FfmpegFound = true
	}
	if p, err := exec.LookPath(render.FfprobeBin()); err == nil {
		report.FfprobePath = p
		report.FfprobeFound = true
	}
	if p, err := exec.LookPath(transcribe.WhisperBin()); err == nil {
		report.WhisperPath = p
		report.WhisperFound = true
	}
	if p, ok := transcribe.ModelPath(); ok {
		report.WhisperModelPath = p
		report.WhisperModelFound = true
	}
	if p, err := exec.LookPath(voiceover.EdgeTTSBin()); err == nil {
		report.EdgeTTSPath = p
		report.EdgeTTSFound = true
	}

	dirs, err := browserManifestDirs()
	if err != nil {
		return report
	}
	id, idErr := ExtensionID(base64Key)

	for browser, dir := range dirs {
		path := filepath.Join(dir, HostName+".json")
		check := ManifestCheck{Browser: browser, Path: path}
		buf, err := os.ReadFile(path) // #nosec G304
		if err != nil {
			check.Issue = "not installed"
			report.Manifests = append(report.Manifests, check)
			continue
		}
		check.Exists = true

		var m Manifest
		if err := json.Unmarshal(buf, &m); err != nil {
			check.Issue = fmt.Sprintf("manifest is not valid JSON: %s", err)
			report.Manifests = append(report.Manifests, check)
			continue
		}
		check.HostPath = m.Path
		if info, err := os.Stat(m.Path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			check.HostExecutable = true
		} else {
			check.Issue = "host path does not point at an executable file"
		}

		if idErr == nil {
			want := fmt.Sprintf("chrome-extension://%s/", id)
			if !slices.Contains(m.AllowedOrigins, want) {
				if check.Issue != "" {
					check.Issue += "; "
				}
				check.Issue += fmt.Sprintf("allowed_origins does not include %s", want)
			}
		}
		report.Manifests = append(report.Manifests, check)
	}
	return report
}
