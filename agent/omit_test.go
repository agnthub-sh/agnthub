package agent_test

import (
	"slices"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
)

func TestOmitKeysRemovesOnlyTheDeclaringLines(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{
			name: "block values, comments and the last key",
			raw: "---\nname: rev # the id\nhooks:\n  Stop:\n    - command: echo\n" +
				"description: Reviews\ntools:\n- Read\n- Grep\nmcpServers:\n- github\n---\nBody\n",
			want: "---\nname: rev # the id\ndescription: Reviews\ntools:\n- Read\n- Grep\n---\nBody\n",
		},
		{
			name: "CRLF and BOM kept",
			raw:  "\uFEFF---\r\nname: rev\r\npermissionMode: plan\r\ndescription: x\r\n---\r\nBody\r\n",
			want: "\uFEFF---\r\nname: rev\r\ndescription: x\r\n---\r\nBody\r\n",
		},
		{
			name: "flat header with unquoted colons",
			raw:  "---\nname: rev\ndescription: Use when: reviewing\ninitialPrompt: go: now\n---\nB\n",
			want: "---\nname: rev\ndescription: Use when: reviewing\n---\nB\n",
		},
		{
			name: "structured header with unquoted colons",
			raw: "---\nname: rev\ndescription: Use when: reviewing\npermissionMode: ask\n" +
				"hooks:\n  Stop:\n    - command: echo\ndisallowedTools:\n  - Write\n---\nB\n",
			want: "---\nname: rev\ndescription: Use when: reviewing\ndisallowedTools:\n  - Write\n---\nB\n",
		},
	} {
		out, omitted, err := agent.OmitKeys("a.md", []byte(tc.raw),
			"hooks", "mcpServers", "permissionMode", "initialPrompt")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if string(out) != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, out, tc.want)
		}
		if len(omitted) == 0 {
			t.Errorf("%s: nothing reported omitted", tc.name)
		}
	}
}

func TestOmitKeysWithoutTheKeysReturnsTheInput(t *testing.T) {
	raw := []byte("---\nname: rev\ndescription: x\n---\nB\n")
	out, omitted, err := agent.OmitKeys("a.md", raw, "hooks")
	if err != nil || omitted != nil || string(out) != string(raw) {
		t.Fatalf("out=%q omitted=%v err=%v", out, omitted, err)
	}
}

func TestOmitKeysRefusesHeadersItCannotEditByLine(t *testing.T) {
	raw := []byte("---\n{name: rev, description: x, hooks: {Stop: []}}\n---\nB\n")
	if _, _, err := agent.OmitKeys("a.md", raw, "hooks"); err == nil {
		t.Fatal("a flow mapping cannot lose one key by deleting lines")
	}
	out, omitted, err := agent.OmitKeys("a.md",
		[]byte("---\nname: rev\nhooks: x\ndescription: y\n---\nB\n"), "description", "hooks")
	if err != nil || !slices.Equal(omitted, []string{"description", "hooks"}) ||
		string(out) != "---\nname: rev\n---\nB\n" {
		t.Fatalf("out=%q omitted=%v err=%v", out, omitted, err)
	}
}
