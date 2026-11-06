package cli_test

import (
	"strings"
	"testing"
)

// Configurations that still start the removed local MCP server fail with the way to the
// hosted one, whatever flags they pass, and the command stays out of the help.
func TestMCPPointsToTheHostedServer(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"mcp"}, {"mcp", "--api-url", "http://localhost:8080"}} {
		code, out := h.run(args...)
		if code == 0 || !strings.Contains(out, "https://mcp.agnthub.sh") ||
			!strings.Contains(out, "removed in v0.5.0") {
			t.Fatalf("%v: exit %d: %s", args, code, out)
		}
	}
	if _, out := h.run("--help"); strings.Contains(out, "mcp") {
		t.Fatalf("help lists mcp:\n%s", out)
	}
}
