package agent_test

import (
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
)

func TestReplaceKeyRewritesOnlyTheDeclaringLines(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{
			name: "block scalar between keys",
			raw: "---\nname: rev # the id\ndescription: >-\n  Reviews\n  code\nreadonly: true\n" +
				"---\nBody\n",
			want: "---\nname: rev # the id\ndescription: Reviews code\nreadonly: true\n---\nBody\n",
		},
		{
			name: "CRLF and BOM kept, last key",
			raw:  "\uFEFF---\r\nname: rev\r\ndescription: |\r\n  Reviews\r\n  code\r\n---\r\nBody\r\n",
			want: "\uFEFF---\r\nname: rev\r\ndescription: Reviews code\r\n---\r\nBody\r\n",
		},
	} {
		out, err := agent.ReplaceKey("a.md", []byte(tc.raw), "description",
			"description: Reviews code")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if string(out) != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, out, tc.want)
		}
	}
}

func TestReplaceKeyRefusesEditsThatChangeOtherKeys(t *testing.T) {
	for name, tc := range map[string]struct{ raw, line string }{
		"absent key":          {"---\nname: rev\n---\nB\n", "description: x"},
		"line declaring more": {"---\nname: rev\ndescription: d\n---\nB\n", "description: x\nreadonly: true"},
		"line that is no key": {"---\nname: rev\ndescription: d\n---\nB\n", "readonly: true"},
	} {
		if _, err := agent.ReplaceKey("a.md", []byte(tc.raw), "description", tc.line); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
