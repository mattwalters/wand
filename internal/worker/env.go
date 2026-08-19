package worker

import (
	"path/filepath"
	"strings"
)

// strippedVars are the environment variables removed from every child
// environment before an adapter sees it. Names are matched
// case-insensitively: Windows resolves environment variables without
// regard to case, so `Gh_Token` would reach gh in the child just as
// surely as `GH_TOKEN` — an exact-case match is no strip at all there.
//
// This is a denylist, not an allowlist, on purpose: a worker still needs
// its own harness's auth, PATH, HOME and locale, and an allowlist would
// break silently each time a harness grew a new legitimate variable —
// which for isolation code means failing toward "worker cannot run" but
// for the operator means turning the isolation off to make it run again.
// What must never cross the seam is exactly the orchestrator's write
// credentials: Linear and GitHub. SSH_AUTH_SOCK is in the list because
// the orchestrator's ssh-agent is a GitHub write credential in all but
// name — a worker holding the socket can `git push` as the operator.
// Ambient credentials living outside the environment entirely (gh's
// config-dir token) are closed by ChildEnviron below; what remains
// harness-specific (inherited MCP connectors) is each adapter's own,
// proven by the conformance suite.
var strippedVars = map[string]bool{
	"LINEAR_API_KEY":          true,
	"GITHUB_TOKEN":            true,
	"GH_TOKEN":                true,
	"GH_ENTERPRISE_TOKEN":     true,
	"GITHUB_ENTERPRISE_TOKEN": true,
	"SSH_AUTH_SOCK":           true,
}

// StripCredentials returns environ without the orchestrator's write
// credentials. Order is preserved; the input is not modified.
func StripCredentials(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if strippedVars[strings.ToUpper(name)] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// ChildEnviron builds the environment Run hands every adapter: environ with
// the orchestrator's write credentials stripped, plus the harness-agnostic
// closures for ambient credentials that live outside the environment — gh
// keeps its token in a config directory, so GH_CONFIG_DIR is pointed into
// the run's scratch directory, where no token lives, and gh runs
// unauthenticated. These closures live here in the shared runner because
// the ambient credentials exist on the machine no matter which harness
// runs; an adapter that forgot to re-add one would otherwise pass the
// conformance suite green. Adapters close only what is specific to their
// own harness.
func ChildEnviron(spec Spec, environ []string) []string {
	out := StripCredentials(environ)
	return append(out, "GH_CONFIG_DIR="+filepath.Join(spec.ScratchDir, "gh-config"))
}
