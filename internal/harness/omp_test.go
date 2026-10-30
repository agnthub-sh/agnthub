package harness_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/install"
	"github.com/heynemann/agnthub/internal/platform/env"
	"github.com/heynemann/agnthub/internal/state"
)

func ompInstaller(t *testing.T) harness.Installer {
	t.Helper()
	installer, err := harness.Default().Installer(agent.OMP)
	if err != nil {
		t.Fatal(err)
	}
	return installer
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOMPGlobalProfilePrecedence(t *testing.T) {
	home := t.TempDir()
	mkdirs(t,
		filepath.Join(home, ".omp", "profiles", "hep"),
		filepath.Join(home, ".omp", "profiles", "work"),
		filepath.Join(home, ".alt", "profiles", "hep"),
	)
	defaultDir := filepath.Join(home, ".omp", "agent", "agents")
	hepDir := filepath.Join(home, ".omp", "profiles", "hep", "agent", "agents")
	workDir := filepath.Join(home, ".omp", "profiles", "work", "agent", "agents")
	cases := map[string]struct {
		env      map[string]string
		settings harness.Settings
		want     string
	}{
		"nothing selects the default profile": {nil, nil, defaultDir},
		"OMP_PROFILE is the detected default": {
			map[string]string{"OMP_PROFILE": "hep"}, nil, hepDir,
		},
		"explicit profile overrides OMP_PROFILE": {
			map[string]string{"OMP_PROFILE": "hep"}, harness.Settings{"profile": "work"}, workDir,
		},
		"explicit default overrides OMP_PROFILE": {
			map[string]string{
				"OMP_PROFILE": "hep",
			},
			harness.Settings{"profile": "default"},
			defaultDir,
		},
		"legacy PI_PROFILE applies without OMP_PROFILE": {
			map[string]string{"PI_PROFILE": "hep"}, nil, hepDir,
		},
		"empty OMP_PROFILE selects default over PI_PROFILE": {
			map[string]string{"OMP_PROFILE": "", "PI_PROFILE": "hep"}, nil, defaultDir,
		},
		"OMP_PROFILE wins over PI_PROFILE": {
			map[string]string{"OMP_PROFILE": "work", "PI_PROFILE": "hep"}, nil, workDir,
		},
		"named profile ignores PI_CODING_AGENT_DIR": {
			map[string]string{
				"OMP_PROFILE":         "hep",
				"PI_CODING_AGENT_DIR": "/elsewhere",
			},
			nil,
			hepDir,
		},
		"profile-derived PI_CODING_AGENT_DIR is not an override": {
			map[string]string{
				"OMP_PROFILE":         "",
				"PI_PROFILE":          "hep",
				"PI_CODING_AGENT_DIR": filepath.Join(home, ".omp", "profiles", "hep", "agent"),
			}, nil, defaultDir,
		},
		"PI_CONFIG_DIR renames the config root": {
			map[string]string{"PI_CONFIG_DIR": ".alt", "OMP_PROFILE": "hep"},
			nil,
			filepath.Join(home, ".alt", "profiles", "hep", "agent", "agents"),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			vars := map[string]string{"HOME": home}
			for key, value := range tc.env {
				vars[key] = value
			}
			ctx := harness.Context{Env: env.FromMap(vars)}
			dest, err := ompInstaller(t).Destination(ctx, harness.ScopeGlobal, tc.settings)
			if err != nil {
				t.Fatal(err)
			}
			if dest.Dir != tc.want || dest.Shared {
				t.Fatalf("got %+v, want dir %s", dest, tc.want)
			}
		})
	}
}

func TestOMPReportsTheEffectiveProfileAndWhereItCameFrom(t *testing.T) {
	reporter, ok := ompInstaller(t).(harness.SettingsReporter)
	if !ok {
		t.Fatal("omp must report its effective settings")
	}
	cases := map[string]struct {
		vars     map[string]string
		settings harness.Settings
		want     harness.EffectiveSetting
	}{
		"native default": {nil, nil, harness.EffectiveSetting{
			Key: "profile", Value: "default", Source: harness.SettingNativeDefault,
		}},
		"detected": {map[string]string{"OMP_PROFILE": "work"}, nil, harness.EffectiveSetting{
			Key: "profile", Value: "work", Source: "OMP_PROFILE",
		}},
		"explicit wins": {
			map[string]string{"OMP_PROFILE": "work"},
			harness.Settings{"profile": "hep"},
			harness.EffectiveSetting{Key: "profile", Value: "hep", Source: harness.SettingExplicit},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := reporter.EffectiveSettings(
				harness.Context{Env: env.FromMap(tc.vars)},
				tc.settings,
			)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
	invalid := harness.Context{Env: env.FromMap(map[string]string{"OMP_PROFILE": "Bad"})}
	if got := reporter.EffectiveSettings(invalid, nil); got != nil {
		t.Fatalf("an unresolvable profile must not be reported, got %+v", got)
	}
}

func TestOMPRefusesInvalidOrAmbiguousProfiles(t *testing.T) {
	for _, value := range []string{"", "Hep", "hep.", "con", "lpt1.txt", "../hep", "a b"} {
		_, err := harness.ResolveSettings(
			harness.Default(), []string{"omp.profile=" + value}, []agent.HarnessID{agent.OMP},
		)
		if err == nil {
			t.Errorf("profile %q must be refused", value)
		}
	}
	home := t.TempDir()
	installer := ompInstaller(t)
	refused := map[string]map[string]string{
		"invalid detected profile":       {"OMP_PROFILE": "Not Valid"},
		"agent dir relocated by env":     {"PI_CODING_AGENT_DIR": filepath.Join(home, "custom")},
		"named profile does not exist":   {"OMP_PROFILE": "missing"},
		"legacy profile does not exist":  {"PI_PROFILE": "missing"},
		"relative agent dir is unproven": {"PI_CODING_AGENT_DIR": "agent"},
	}
	for name, vars := range refused {
		vars["HOME"] = home
		ctx := harness.Context{Env: env.FromMap(vars)}
		if _, err := installer.Destination(ctx, harness.ScopeGlobal, nil); err == nil {
			t.Errorf("%s: global destination must be refused", name)
		}
	}
	ctx := harness.Context{Env: env.FromMap(map[string]string{
		"HOME": home, "PI_CODING_AGENT_DIR": filepath.Join(home, "custom"),
	})}
	dest, err := installer.Destination(
		ctx,
		harness.ScopeGlobal,
		harness.Settings{"profile": "default"},
	)
	if err != nil {
		t.Fatalf("explicit profile must resolve the ambiguity: %v", err)
	}
	if dest.Dir != filepath.Join(home, ".omp", "agent", "agents") {
		t.Fatalf("explicit default must target the discovered agent directory, got %s", dest.Dir)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("resolving destinations must not create anything: %v %v", entries, err)
	}
}

const ompPortable = `---
name: reviewer
description: Reviews code
---
Review the diff carefully.
`

type ompFixture struct {
	t       *testing.T
	home    string
	project string
	planner *install.Planner
}

func newOMPFixture(t *testing.T) *ompFixture {
	t.Helper()
	base := t.TempDir()
	f := &ompFixture{
		t:       t,
		home:    filepath.Join(base, "home"),
		project: filepath.Join(base, "project"),
	}
	mkdirs(t, f.home, f.project)
	lockPath := filepath.Join(f.project, state.LockFileName)
	lock, err := state.LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := state.LoadOwnership(state.OwnershipPath(filepath.Join(base, "state")))
	if err != nil {
		t.Fatal(err)
	}
	f.planner = &install.Planner{
		Registry: harness.Default(), Lock: lock, LockPath: lockPath, Ownership: ownership,
	}
	return f
}

func (f *ompFixture) install(
	scope harness.Scope,
	settings harness.Settings,
) (*install.Plan, error) {
	f.t.Helper()
	doc, err := agent.ParseMarkdown("agents/reviewer.md", []byte(ompPortable))
	if err != nil {
		f.t.Fatal(err)
	}
	plan, err := f.planner.Build(install.Request{
		Source: state.LockedSource{Kind: "local", Locator: "/src"},
		Targets: []install.Target{
			{
				Definition: &agent.Definition{ID: "reviewer", Portable: doc},
				Origin:     state.OriginDirect,
			},
		},
		Harnesses: []agent.HarnessID{agent.OMP},
		Settings:  map[agent.HarnessID]harness.Settings{agent.OMP: settings},
		Scope:     scope,
		Context: harness.Context{
			Env:         env.FromMap(map[string]string{"HOME": f.home}),
			ProjectRoot: f.project,
		},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	_, err = f.planner.Apply(plan, f.project, time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC))
	return plan, err
}

func TestOMPGlobalInstallNeverInitializesProfile(t *testing.T) {
	f := newOMPFixture(t)
	plan, err := f.install(harness.ScopeGlobal, harness.Settings{"profile": "hep"})
	if !errors.Is(err, install.ErrBlocked) || plan.Items[0].Action != install.ActionIncompatible {
		t.Fatalf("missing profile must block before writing, got %v %+v", err, plan.Items)
	}
	if _, statErr := os.Stat(filepath.Join(f.home, ".omp")); !os.IsNotExist(statErr) {
		t.Fatal("a refused install must not create omp config")
	}

	profileRoot := filepath.Join(f.home, ".omp", "profiles", "hep")
	existing := map[string]string{
		"config.yml":     "modelRoles: {}\n",
		"agent/.env":     "SECRET=1\n",
		"agent/agent.db": "auth",
	}
	for rel, content := range existing {
		path := filepath.Join(profileRoot, rel)
		mkdirs(t, filepath.Dir(path))
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.install(harness.ScopeGlobal, harness.Settings{"profile": "hep"}); err != nil {
		t.Fatal(err)
	}
	var files []string
	err = filepath.WalkDir(profileRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		rel, _ := filepath.Rel(profileRoot, path)
		files = append(files, filepath.ToSlash(rel))
		content, _ := os.ReadFile(path)
		if want, ok := existing[filepath.ToSlash(rel)]; ok && string(content) != want {
			t.Errorf("%s was modified", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	want := []string{"agent/.env", "agent/agent.db", "agent/agents/reviewer.md", "config.yml"}
	if !slices.Equal(files, want) {
		t.Fatalf("profile content changed beyond the agent file: %v", files)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".omp", "agent")); !os.IsNotExist(err) {
		t.Fatal("a named-profile install must not touch the default profile")
	}
}

func TestOMPProjectFileIsSharedAcrossProfiles(t *testing.T) {
	f := newOMPFixture(t)
	first, err := f.install(harness.ScopeProject, harness.Settings{"profile": "hep"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.install(harness.ScopeProject, harness.Settings{"profile": "work"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.project, ".omp", "agents", "reviewer.md")
	if first.Items[0].Path != path || second.Items[0].Path != path ||
		!second.Items[0].Destination.Shared {
		t.Fatalf(
			"both profiles must target the shared project file: %+v / %+v",
			first.Items[0],
			second.Items[0],
		)
	}
	if second.Items[0].Action != install.ActionUnchanged {
		t.Fatalf("second profile must reuse the shared file, got %s", second.Items[0].Action)
	}
	var files []string
	_ = filepath.WalkDir(f.project, func(p string, entry os.DirEntry, _ error) error {
		if entry != nil && !entry.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	want := []string{path, filepath.Join(f.project, state.LockFileName)}
	slices.Sort(files)
	slices.Sort(want)
	if !slices.Equal(files, want) {
		t.Fatalf("profiles must not duplicate project artifacts: %v", files)
	}
	records := f.planner.Ownership.ForPath(path)
	profiles := make([]string, 0, len(records))
	for _, record := range records {
		profiles = append(profiles, record.Settings["profile"])
	}
	slices.Sort(profiles)
	if !slices.Equal(profiles, []string{"hep", "work"}) {
		t.Fatalf("want one binding per profile, got %+v", records)
	}
}

func TestOMPNativeVariantsStayFaithful(t *testing.T) {
	installer := ompInstaller(t)
	native := []byte(
		"---\nname: reviewer\ndescription: Reviews code\ntools: [read, grep]\nmodel: \"@review\"\n---\nBody\n",
	)
	artifact, err := installer.Native(
		"reviewer",
		&agent.VariantFile{Path: "omp/reviewer.md", Raw: native},
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(artifact.Content) != string(native) || artifact.Generated ||
		len(artifact.Notes) == 0 {
		t.Fatalf("native variant must be copied verbatim with a tools advisory: %+v", artifact)
	}
	invalid := map[string]string{
		"name mismatch":  "---\nname: other\ndescription: d\n---\nBody\n",
		"reserved name":  "---\nname: main\ndescription: d\n---\nBody\n",
		"no description": "---\nname: reviewer\n---\nBody\n",
	}
	for name, raw := range invalid {
		id := "reviewer"
		if name == "reserved name" {
			id = "main"
		}
		_, err := installer.Native(id, &agent.VariantFile{Path: "omp/x.md", Raw: []byte(raw)})
		var invalidErr *harness.InvalidVariantError
		if !errors.As(err, &invalidErr) {
			t.Errorf("%s: want InvalidVariantError, got %v", name, err)
		}
	}
	doc, err := agent.ParseMarkdown("agents/reviewer.md",
		[]byte("---\nname: reviewer\ndescription: d\ntools: [read]\n---\nBody\n"))
	if err != nil {
		t.Fatal(err)
	}
	var incompatible *harness.IncompatibleError
	if _, err := convert(installer, "reviewer", doc); !errors.As(err, &incompatible) {
		t.Fatalf("portable tools must refuse conversion, got %v", err)
	}
}
