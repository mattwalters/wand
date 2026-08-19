package cli

import "testing"

func TestVersionString(t *testing.T) {
	got := versionString("1.2.3", "abc1234", "2026-08-18T00:00:00Z", 1)
	want := "wand 1.2.3\n" +
		"  commit:          abc1234\n" +
		"  built:           2026-08-18T00:00:00Z\n" +
		"  covenant schema: 1\n"
	if got != want {
		t.Errorf("versionString:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// The unstamped defaults are goreleaser's conventions ("dev", "none",
// "unknown"); the stamping ldflags in .goreleaser.yml and the var names
// here must agree or releases silently report a dev build.
func TestVersionDefaults(t *testing.T) {
	if Version != "dev" || Commit != "none" || Date != "unknown" {
		t.Errorf("unstamped build defaults = %q, %q, %q; want dev, none, unknown", Version, Commit, Date)
	}
}
