package voiceover

import "testing"

func TestDefaultVoiceForLanguage(t *testing.T) {
	tests := []struct {
		lang string
		want string
	}{
		{"ru", "ru-RU-DmitryNeural"},
		{"en", defaultTTSVoice},
		{"ja", "ja-JP-KeitaNeural"},
		// Unrecognized, "auto", and "" all fall back the same way: narrating in the wrong
		// voice beats not narrating a session at all just because its language isn't in
		// languageVoices yet.
		{"xx", defaultTTSVoice},
		{"auto", defaultTTSVoice},
		{"", defaultTTSVoice},
	}
	for _, tt := range tests {
		if got := DefaultVoiceForLanguage(tt.lang); got != tt.want {
			t.Errorf("DefaultVoiceForLanguage(%q) = %q, want %q", tt.lang, got, tt.want)
		}
	}
}
