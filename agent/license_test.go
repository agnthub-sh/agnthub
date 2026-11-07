package agent_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
)

func TestParseLicenseKeepsTheCopyrightNotices(t *testing.T) {
	cases := map[string]struct {
		text string
		want []string
		more bool
	}{
		"MIT": {
			text: "\uFEFFMIT License\r\n\r\nCopyright (c) 2025 Jane Doe\r\n\r\nPermission is " +
				"hereby granted, free of charge, provided that the above\r\ncopyright notice " +
				"and this permission notice are included.\r\n",
			want: []string{"Copyright (c) 2025 Jane Doe"},
		},
		"Apache appendix template": {
			text: "   APPENDIX\n\n      Copyright [yyyy] [name of copyright owner]\n" +
				"      copyright owner or entity\n",
		},
		"GPL how-to template": {
			text: "    Copyright (C) <year>  <name of author>\n",
		},
		"several holders": {
			text: "© 2020 A\n(c) 2021 B\nCOPYRIGHT 2022 C\nCopyright 2023 D\n" +
				"Copyright 2024 E\nCopyright 2025 F\n",
			want: []string{
				"© 2020 A", "(c) 2021 B", "COPYRIGHT 2022 C", "Copyright 2023 D",
				"Copyright 2024 E",
			},
			more: true,
		},
	}
	for name, tc := range cases {
		got := agent.ParseLicense("LICENSE", []byte(tc.text))
		if got.Path != "LICENSE" || !slices.Equal(got.Copyright, tc.want) || got.More != tc.more {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestParseLicenseKeepsEachNoticeOnOneBoundedLine(t *testing.T) {
	long := "Copyright (c) 2025 " + strings.Repeat("é", 300) + "\x1b[2J\n"
	got := agent.ParseLicense("LICENSE", []byte(long))
	if len(got.Copyright) != 1 {
		t.Fatalf("%+v", got)
	}
	line := got.Copyright[0]
	if len(line) > 210 || strings.ContainsAny(line, "\x1b\n\r") || !strings.HasSuffix(line, "…") {
		t.Fatalf("notice not bounded to one printable line: %q", line)
	}
}
