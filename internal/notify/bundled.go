package notify

import (
	"embed"
	"os"
	"path/filepath"
)

// The two notification sounds herdr ships (assets/sounds, Apache-2.0; see NOTICE), transcoded
// to 22.05 kHz mono WAV so that every player decodes them — PulseAudio's paplay/pw-play only
// play mp3 through libsndfile 1.1+, which RHEL 9 does not have.
//
//go:embed sounds/done.wav sounds/request.wav
var bundled embed.FS

var bundledByKind = map[string]string{"done": "done.wav", "blocked": "request.wav", "error": "request.wav"}

// BundledFiles returns kind -> path for the sounds shipped in the binary, written under
// <stateDir>/sounds/bundled/ when missing or stale (the players need a file on disk).
func BundledFiles(stateDir string) map[string]string {
	dir := filepath.Join(stateDir, "sounds", "bundled")
	_ = os.MkdirAll(dir, 0o755)
	out := map[string]string{}
	for kind, name := range bundledByKind {
		data, err := bundled.ReadFile("sounds/" + name)
		if err != nil {
			continue
		}
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err != nil || fi.Size() != int64(len(data)) {
			if err := os.WriteFile(p, data, 0o644); err != nil {
				continue
			}
		}
		out[kind] = p
	}
	return out
}

// Resolve lays the configured paths over the bundled defaults: an empty configured path
// means the bundled sound for that kind.
func Resolve(stateDir string, configured map[string]string) map[string]string {
	files := BundledFiles(stateDir)
	for kind, path := range configured {
		if path != "" {
			files[kind] = path
		}
	}
	return files
}
