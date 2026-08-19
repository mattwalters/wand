//go:build conformance

package worker_test

import (
	"os/exec"
	"testing"

	"github.com/mattwalters/wand/internal/worker"
	"github.com/mattwalters/wand/internal/workertest"
)

// TestClaudeCodeIsolation is the live half of the conformance suite: it
// spawns the real claude binary and proves a worker instructed to write
// Linear fails. Behind the `conformance` tag (make test-conformance)
// because it spends a real model call and needs the harness installed and
// authenticated. A missing binary is a failure, not a skip — running this
// tag means asking for proof, and a skip would read as proof from a
// distance.
func TestClaudeCodeIsolation(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Fatal("claude is not on PATH; the conformance run proves the real harness or it proves nothing")
	}
	workertest.Isolation(t, worker.ClaudeCode{})
}
