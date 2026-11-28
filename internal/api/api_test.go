package api

import (
	"os"
	"strings"
	"testing"

	"github.com/bragibooks/bragibooks/internal/audio"
)

// configEnvPrefixes are the section prefixes of the deployment environment
// the config layer reads through viper's AutomaticEnv (SECTION_KEY maps to
// section.key, e.g. DIRECTORIES_OUTPUT_DIR -> directories.output_dir) plus
// the explicitly bound region variable. Tests build their config from
// explicit temp YAML; ambient deployment vars must never leak in — they
// point at container paths (/output, /input/done) that do not exist on the
// dev machine and would fail the queue-time write probes.
var configEnvPrefixes = []string{
	"SERVER_",
	"DIRECTORIES_",
	"PROCESSING_",
	"M4B_MERGE_",
	"API_KEY_",
	"DATABASE_",
	"REGION",
}

// TestMain isolates the suite from the ambient deployment environment:
// every config-driven env var is unset before any test runs, so the suite
// behaves identically on a dev machine, in the pre-commit hook, and in CI
// regardless of which of those vars the host shell happens to export. It
// also installs the engine's verified path-token vocabulary (main.go does
// this after CheckCapabilities): path-format validation is
// capability-driven, so a test that exercises it needs the engine's tokens.
func TestMain(m *testing.M) {
	audio.SetPathFormatTokens([]string{
		"author", "authors", "narrator", "narrators", "title", "subtitle",
		"series_name", "series_position", "year", "genre", "metadata_id", "book_id",
	})
	for _, kv := range os.Environ() {
		name := kv[:strings.IndexByte(kv, '=')]
		for _, p := range configEnvPrefixes {
			if strings.HasPrefix(name, p) {
				os.Unsetenv(name)
				break
			}
		}
	}
	os.Exit(m.Run())
}
