package worker

import "strings"

// strippedVars are the environment variables removed from every child
// environment before an adapter sees it.
//
// This is a denylist, not an allowlist, on purpose: a worker still needs
// its own harness's auth, PATH, HOME and locale, and an allowlist would
// break silently each time a harness grew a new legitimate variable —
// which for isolation code means failing toward "worker cannot run" but
// for the operator means turning the isolation off to make it run again.
// What must never cross the seam is exactly the orchestrator's write
// credentials: Linear and GitHub. Credentials living outside the
// environment (gh's config-dir token, MCP connectors) are each harness's
// own leak path, closed in its adapter and proven by the conformance
// suite.
var strippedVars = map[string]bool{
	"LINEAR_API_KEY":      true,
	"GITHUB_TOKEN":        true,
	"GH_TOKEN":            true,
	"GH_ENTERPRISE_TOKEN": true,
}

// StripCredentials returns environ without the orchestrator's write
// credentials. Order is preserved; the input is not modified.
func StripCredentials(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if strippedVars[name] {
			continue
		}
		out = append(out, kv)
	}
	return out
}
