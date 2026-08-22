package render

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// The three files below are CC0 assets. Their sources and authors are recorded
// in assets/NOTICE.md and docs/research-audio-assets.md.
//
//go:embed assets/chill-loopable.mp3
var backgroundMusic []byte

//go:embed assets/click.wav
var clickSound []byte

//go:embed assets/typing.wav
var typingSound []byte

type audioAssets struct {
	Music  string
	Click  string
	Typing string
}

// writeAudioAssets materializes the embedded assets alongside a render. FFmpeg
// takes paths rather than byte streams, and the temporary directory is removed
// together with the other render intermediates after success.
func writeAudioAssets(dir string) (audioAssets, error) {
	assetDir := filepath.Join(dir, tmpDir, "audio")
	if err := os.MkdirAll(assetDir, 0o750); err != nil { // #nosec G301
		return audioAssets{}, fmt.Errorf("create asset directory: %w", err)
	}
	files := []struct {
		name string
		data []byte
	}{
		{name: "background.mp3", data: backgroundMusic},
		{name: "click.wav", data: clickSound},
		{name: "typing.wav", data: typingSound},
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(assetDir, file.name), file.data, 0o600); err != nil { // #nosec G306
			return audioAssets{}, fmt.Errorf("write asset: %w", err)
		}
	}
	return audioAssets{
		Music:  filepath.Join(tmpDir, "audio", "background.mp3"),
		Click:  filepath.Join(tmpDir, "audio", "click.wav"),
		Typing: filepath.Join(tmpDir, "audio", "typing.wav"),
	}, nil
}
