package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestAddConvertsPortableAgentForCodex(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	code, out := h.run("add", h.source, "--harness", "codex", "--yes")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	var fields map[string]string
	if err := toml.Unmarshal(
		[]byte(h.installed(".codex/agents/reviewer.toml")),
		&fields,
	); err != nil {
		t.Fatal(err)
	}
	if fields["name"] != "reviewer" ||
		fields["developer_instructions"] != "Review the diff carefully.\n" {
		t.Fatalf("unexpected Codex agent: %#v", fields)
	}
	if _, err := os.Stat(filepath.Join(h.project, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Fatal("installing an agent must not create Codex configuration")
	}
}

func TestAddCodexNativeVariantWinsAndParentOverridesRefuseBeforeWriting(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	native := "name = \"reviewer\"\ndescription = \"Native\"\ndeveloper_instructions = \"Native instructions.\"\n"
	h.write(".codex/agents/reviewer.toml", native)
	if code, out := h.run("add", h.source, "--harness", "codex", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if h.installed(".codex/agents/reviewer.toml") != native {
		t.Fatal("authored Codex variant must install byte-for-byte")
	}

	h2 := newHarness(t)
	h2.write("agents/reviewer.md", reviewer)
	h2.write(".codex/agents/reviewer.toml", native+"sandbox_mode = \"danger-full-access\"\n")
	code, out := h2.run("add", h2.source, "--harness", "codex", "--yes")
	if code == 0 || !strings.Contains(out, "sandbox_mode") {
		t.Fatalf("parent-overriding variant must fail, exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(h2.project, ".codex")); !os.IsNotExist(err) {
		t.Fatal("nothing may be written when a Codex variant is invalid")
	}

	h3 := newHarness(t)
	h3.write("agents/reviewer.md", "---\nname: reviewer\ndescription: x\ntools: Read\n---\nbody\n")
	code, out = h3.run("add", h3.source, "--harness", "codex", "--yes")
	if code == 0 || !strings.Contains(out, `"tools"`) {
		t.Fatalf("declared tools must refuse Codex conversion, exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(h3.project, ".codex")); !os.IsNotExist(err) {
		t.Fatal("nothing may be written when conversion is refused")
	}
}
