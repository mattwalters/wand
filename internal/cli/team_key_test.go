package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/doctor"
)

// writeKeyedWandToml writes a minimal covenant file binding dir to a team.
func writeKeyedWandToml(t *testing.T, dir, key string) {
	t.Helper()
	body := "schema = 1\n[team]\nkey = \"" + key + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "wand.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bothFixes is the substring every "no team key resolvable" error carries,
// naming both the flag and the file as fixes.
const bothFixes = "no team key: pass --team-key, or add [team] key to wand.toml"

func TestResolveTeamKey(t *testing.T) {
	cases := []struct {
		name             string
		explicit, file   string
		want             string
		wantErrSubstring string
	}{
		{name: "flag beats file", explicit: "WND", file: "OTHER", want: "WND"},
		{name: "file supplies the key", explicit: "", file: "WND", want: "WND"},
		{name: "neither present", explicit: "", file: "", wantErrSubstring: bothFixes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveTeamKey(tc.explicit, tc.file)
			if tc.wantErrSubstring != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSubstring) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErrSubstring)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The walk is what lets a command run from a subdirectory see the same
// covenant, and the same team key, as one run at the repo root.
func TestCovenantFromCwdWalksUpForTeamKey(t *testing.T) {
	root := t.TempDir()
	writeKeyedWandToml(t, root, "WND")
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	_, teamKey, fromFile, err := covenantFromCwd()
	if err != nil {
		t.Fatalf("covenantFromCwd: %v", err)
	}
	if !fromFile {
		t.Error("fromFile = false, want the walk to find the root's wand.toml")
	}
	if teamKey != "WND" {
		t.Errorf("teamKey = %q, want %q", teamKey, "WND")
	}
}

func TestCovenantFromCwdNoFileIsStock(t *testing.T) {
	t.Chdir(t.TempDir())

	cov, teamKey, fromFile, err := covenantFromCwd()
	if err != nil {
		t.Fatalf("covenantFromCwd: %v", err)
	}
	if fromFile {
		t.Error("fromFile = true with no wand.toml anywhere up the tree")
	}
	if teamKey != "" {
		t.Errorf("teamKey = %q, want empty", teamKey)
	}
	if len(cov.Statuses) == 0 {
		t.Error("no wand.toml did not yield the stock covenant")
	}
}

// runQueue, runFile and runDoctorCmd below drive each of the five
// team-scoped commands the same way ui_test.go drives ui: through the real
// cobra command, so what is proven is the wiring, not just resolveTeamKey in
// isolation. Every command is checked for team-key resolution up to, but not
// past, the point network I/O would be needed — LINEAR_API_KEY is left
// unset, and the assertion is which error surfaces: "no team key" means
// resolution failed, an error naming LINEAR_API_KEY means it succeeded and
// execution moved on.

func runQueue(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newQueueCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	return out.String(), cmd.Execute()
}

func TestQueueTeamKeyResolution(t *testing.T) {
	t.Run("neither flag nor file", func(t *testing.T) {
		t.Chdir(t.TempDir())
		_, err := runQueue(t)
		if err == nil || !strings.Contains(err.Error(), bothFixes) {
			t.Fatalf("err = %v, want the both-fixes error", err)
		}
	})

	t.Run("file supplies the key", func(t *testing.T) {
		t.Setenv("LINEAR_API_KEY", "")
		dir := t.TempDir()
		writeKeyedWandToml(t, dir, "WND")
		t.Chdir(dir)
		_, err := runQueue(t)
		if err == nil || strings.Contains(err.Error(), "team key") {
			t.Fatalf("err = %v, want resolution to succeed and fail past it", err)
		}
		if !strings.Contains(err.Error(), "LINEAR_API_KEY") {
			t.Errorf("err = %v, want it to mention LINEAR_API_KEY", err)
		}
	})

	t.Run("flag beats file", func(t *testing.T) {
		t.Setenv("LINEAR_API_KEY", "")
		dir := t.TempDir()
		writeKeyedWandToml(t, dir, "OTHER")
		t.Chdir(dir)
		_, err := runQueue(t, "--team-key", "WND")
		if err == nil || strings.Contains(err.Error(), "team key") {
			t.Fatalf("err = %v, want resolution to succeed and fail past it", err)
		}
	})

	t.Run("subdirectory walk resolves", func(t *testing.T) {
		t.Setenv("LINEAR_API_KEY", "")
		root := t.TempDir()
		writeKeyedWandToml(t, root, "WND")
		sub := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(sub)
		_, err := runQueue(t)
		if err == nil || strings.Contains(err.Error(), "team key") {
			t.Fatalf("err = %v, want the walk to find the root's wand.toml", err)
		}
	})
}

func runFile(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newFileCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"a finding"}, args...))
	return out.String(), cmd.Execute()
}

func TestFileTeamKeyResolution(t *testing.T) {
	t.Run("neither flag nor file", func(t *testing.T) {
		t.Chdir(t.TempDir())
		_, err := runFile(t)
		if err == nil || !strings.Contains(err.Error(), bothFixes) {
			t.Fatalf("err = %v, want the both-fixes error", err)
		}
	})

	t.Run("file supplies the key", func(t *testing.T) {
		t.Setenv("LINEAR_API_KEY", "")
		dir := t.TempDir()
		writeKeyedWandToml(t, dir, "WND")
		t.Chdir(dir)
		_, err := runFile(t)
		if err == nil || strings.Contains(err.Error(), "team key") {
			t.Fatalf("err = %v, want resolution to succeed and fail past it", err)
		}
		if !strings.Contains(err.Error(), "LINEAR_API_KEY") {
			t.Errorf("err = %v, want it to mention LINEAR_API_KEY", err)
		}
	})

	t.Run("flag beats file", func(t *testing.T) {
		t.Setenv("LINEAR_API_KEY", "")
		dir := t.TempDir()
		writeKeyedWandToml(t, dir, "OTHER")
		t.Chdir(dir)
		_, err := runFile(t, "--team-key", "WND")
		if err == nil || strings.Contains(err.Error(), "team key") {
			t.Fatalf("err = %v, want resolution to succeed and fail past it", err)
		}
	})

	t.Run("subdirectory walk resolves", func(t *testing.T) {
		t.Setenv("LINEAR_API_KEY", "")
		root := t.TempDir()
		writeKeyedWandToml(t, root, "WND")
		sub := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(sub)
		_, err := runFile(t)
		if err == nil || strings.Contains(err.Error(), "team key") {
			t.Fatalf("err = %v, want the walk to find the root's wand.toml", err)
		}
	})
}

func runInit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newInitCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	return out.String(), cmd.Execute()
}

func TestInitTeamKeyResolution(t *testing.T) {
	t.Run("neither flag nor file: still a hard required-flag error", func(t *testing.T) {
		t.Chdir(t.TempDir())
		_, err := runInit(t)
		if err == nil || !strings.Contains(err.Error(), bothFixes) {
			t.Fatalf("err = %v, want the both-fixes error", err)
		}
	})

	t.Run("a re-run with wand.toml already keyed does not need the flag", func(t *testing.T) {
		t.Setenv("LINEAR_API_KEY", "")
		dir := t.TempDir()
		writeKeyedWandToml(t, dir, "WND")
		t.Chdir(dir)
		_, err := runInit(t)
		if err == nil || strings.Contains(err.Error(), "team key") {
			t.Fatalf("err = %v, want resolution to succeed and fail past it", err)
		}
		if !strings.Contains(err.Error(), "LINEAR_API_KEY") {
			t.Errorf("err = %v, want it to mention LINEAR_API_KEY", err)
		}
	})

	// init must walk up from cwd the same as doctor, queue, ui and file: a
	// re-run from a nested package directory has to see the repo root's
	// wand.toml, not just a re-run from the root itself.
	t.Run("subdirectory walk resolves", func(t *testing.T) {
		t.Setenv("LINEAR_API_KEY", "")
		root := t.TempDir()
		writeKeyedWandToml(t, root, "WND")
		sub := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(sub)
		_, err := runInit(t)
		if err == nil || strings.Contains(err.Error(), "team key") {
			t.Fatalf("err = %v, want the walk to find the root's wand.toml", err)
		}
		if !strings.Contains(err.Error(), "LINEAR_API_KEY") {
			t.Errorf("err = %v, want it to mention LINEAR_API_KEY", err)
		}
	})
}

func TestDoctorTeamKeyResolution(t *testing.T) {
	newCmd := func() *cobra.Command {
		cmd := &cobra.Command{Use: "doctor-test"}
		cmd.SetContext(context.Background())
		return cmd
	}

	t.Run("neither flag nor file", func(t *testing.T) {
		t.Chdir(t.TempDir())
		cmd := newCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		code := runDoctor(cmd, "")
		if code != doctor.ExitError {
			t.Fatalf("code = %d, want %d", code, doctor.ExitError)
		}
		if !strings.Contains(out.String(), bothFixes) {
			t.Errorf("output = %q, want the both-fixes error", out.String())
		}
	})

	t.Run("file supplies the key", func(t *testing.T) {
		t.Setenv("LINEAR_API_KEY", "")
		dir := t.TempDir()
		writeKeyedWandToml(t, dir, "WND")
		t.Chdir(dir)
		cmd := newCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		code := runDoctor(cmd, "")
		if code != doctor.ExitError {
			t.Fatalf("code = %d, want %d", code, doctor.ExitError)
		}
		if strings.Contains(out.String(), "team key") {
			t.Fatalf("output = %q, want resolution to succeed and fail past it", out.String())
		}
		if !strings.Contains(out.String(), "LINEAR_API_KEY") {
			t.Errorf("output = %q, want it to mention LINEAR_API_KEY", out.String())
		}
	})

	t.Run("flag beats file", func(t *testing.T) {
		t.Setenv("LINEAR_API_KEY", "")
		dir := t.TempDir()
		writeKeyedWandToml(t, dir, "OTHER")
		t.Chdir(dir)
		cmd := newCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		code := runDoctor(cmd, "WND")
		if code != doctor.ExitError {
			t.Fatalf("code = %d, want %d", code, doctor.ExitError)
		}
		if strings.Contains(out.String(), "team key") {
			t.Fatalf("output = %q, want resolution to succeed and fail past it", out.String())
		}
	})

	t.Run("subdirectory walk resolves", func(t *testing.T) {
		t.Setenv("LINEAR_API_KEY", "")
		root := t.TempDir()
		writeKeyedWandToml(t, root, "WND")
		sub := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(sub)
		cmd := newCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		code := runDoctor(cmd, "")
		if code != doctor.ExitError {
			t.Fatalf("code = %d, want %d", code, doctor.ExitError)
		}
		if strings.Contains(out.String(), "team key") {
			t.Fatalf("output = %q, want the walk to find the root's wand.toml", out.String())
		}
	})
}
