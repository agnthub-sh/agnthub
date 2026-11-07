package harness_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

const catalogID = "engineering-search-relevance-engineer"

// catalogCore is an agency-agents file: a display name and cosmetic keys.
func catalogCore(t *testing.T) *agent.Document {
	t.Helper()
	raw := "---\nname: Search Relevance Engineer\n" +
		"description: Expert search engineer for Elasticsearch and OpenSearch\n" +
		"color: \"#00BFB3\"\nemoji: 🔎\nvibe: Recall finds it, precision ranks it.\n---\n" +
		"# Search Relevance Engineer\n\nTune relevance.\n"
	doc, err := agent.ParseMarkdownLenient("engineering/"+catalogID+".md", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// Generated files name the agent by its identifier wherever a harness
// invokes agents by name, keep the display name in a display field where
// one exists, and lead the description with it elsewhere; cosmetic keys are
// dropped with a note, never refused.
func TestConversionCarriesDisplayNamesPerHarness(t *testing.T) {
	const label = "Search Relevance Engineer"
	for _, target := range agent.AllHarnesses {
		t.Run(string(target), func(t *testing.T) {
			artifact, err := convert(installer(t, target), catalogID, catalogCore(t))
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			content := string(artifact.Content)
			for _, key := range []string{"color", "emoji", "vibe"} {
				if strings.Contains(content, key+":") || strings.Contains(content, key+" =") {
					t.Errorf("cosmetic %s carried:\n%s", key, content)
				}
			}
			adapter, _ := harness.Default().Get(target)
			identity, err := adapter.Identify(artifact.FileName, artifact.Content)
			if err != nil || identity.ID != catalogID {
				t.Fatalf("generated file identifies as %q (%v):\n%s", identity.ID, err, content)
			}
			if name := harness.InvocationName(
				adapter,
				artifact.FileName,
				artifact.Content,
			); name != "" &&
				name != catalogID {
				t.Errorf("%s would invoke it as %q, want the identifier", target, name)
			}
			leads := strings.Contains(content, label+": Expert search engineer")
			switch target {
			case agent.Copilot:
				doc, _ := agent.ParseMarkdown(artifact.FileName, artifact.Content)
				if doc.String("name") != label || leads {
					t.Errorf("Copilot CLI's name is its label:\n%s", content)
				}
			case agent.Gemini:
				doc, _ := agent.ParseMarkdown(artifact.FileName, artifact.Content)
				if doc.String("display_name") != label || doc.String("name") != catalogID || leads {
					t.Errorf("Gemini CLI keeps the label in display_name:\n%s", content)
				}
			default:
				if !leads {
					t.Errorf("the description should start with the display name:\n%s", content)
				}
			}
		})
	}
}

func TestConversionOmitsDisplayNamesThatSpellTheIdentifier(t *testing.T) {
	doc := portableDoc(map[string]any{
		"name": "Code Reviewer", "description": "Reviews code",
	}, "Review.\n")
	artifact, err := convert(installer(t, agent.OMP), "code-reviewer", doc)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := agent.ParseMarkdown(artifact.FileName, artifact.Content)
	if parsed.String("name") != "code-reviewer" || parsed.String("description") != "Reviews code" {
		t.Fatalf("unexpected conversion:\n%s", artifact.Content)
	}
}

// An authored file installs into its own harness byte-identical, display
// name included, wherever that harness accepts it; Gemini CLI does not.
func TestNativeVariantsKeepDisplayNames(t *testing.T) {
	markdown := "---\nname: Search Relevance Engineer\ndescription: Tunes search\n---\nTune.\n"
	cases := map[agent.HarnessID]struct{ path, raw string }{
		agent.Claude:  {".claude/agents/search-relevance-engineer.md", markdown},
		agent.OMP:     {".omp/agents/search-relevance-engineer.md", markdown},
		agent.Cursor:  {".cursor/agents/Search Relevance Engineer.md", markdown},
		agent.Copilot: {".github/agents/search-relevance-engineer.agent.md", markdown},
		agent.Codex: {
			".codex/agents/search-relevance-engineer.toml",
			"name = \"Search Relevance Engineer\"\ndescription = \"Tunes search\"\n" +
				"developer_instructions = \"Tune.\"\n",
		},
		agent.Gemini: {".gemini/agents/search-relevance-engineer.md", markdown},
	}
	for target, tc := range cases {
		t.Run(string(target), func(t *testing.T) {
			adapter, _ := harness.Default().Get(target)
			identity, _ := adapter.Identify(tc.path, []byte(tc.raw))
			if identity.ID != "search-relevance-engineer" {
				t.Fatalf("identity %q", identity.ID)
			}
			variant := &agent.VariantFile{Harness: target, Path: tc.path, Raw: []byte(tc.raw)}
			artifact, err := installer(t, target).Native(identity.ID, variant)
			if target == agent.Gemini {
				var invalid *harness.InvalidVariantError
				if !errors.As(err, &invalid) {
					t.Fatalf(
						"Gemini CLI rejects a display name; want an invalid variant, got %v",
						err,
					)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(artifact.Content) != tc.raw {
				t.Fatalf("authored file changed:\n%s", artifact.Content)
			}
		})
	}
}
