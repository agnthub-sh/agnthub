// Package skills plans, installs and independently verifies Skill Dependencies:
// it finds visible existing Skills in each Harness's effective precedence order,
// validates the requested source revision against strict metadata.version bounds,
// runs the user's trusted installer backend and verifies what it actually placed.
package skills

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/platform/env"
)

// Placeholders substituted into installer arguments.
const (
	PlaceholderSource = "{source}"
	PlaceholderSkill  = "{skill}"
	PlaceholderTarget = "{target}"
)

// Installer is the trusted user-configured Skill installer backend: one
// executable and argument templates, run directly without a shell.
type Installer struct {
	Executable  string   `yaml:"executable"`
	Args        []string `yaml:"args"`
	ProjectArgs []string `yaml:"projectArgs"`
	GlobalArgs  []string `yaml:"globalArgs"`
	// Origin is the configuration file the installer came from, or "" for the default.
	Origin string `yaml:"-"`
}

// DefaultInstaller is skills.sh, the documented default backend.
func DefaultInstaller() Installer {
	return Installer{
		Executable: "npx",
		Args: []string{
			"--yes", "skills", "add", PlaceholderSource,
			"--skill", PlaceholderSkill, "--agent", PlaceholderTarget, "--yes",
		},
		ProjectArgs: []string{},
		GlobalArgs:  []string{"--global"},
	}
}

// ConfigPath is the user-level agnthub configuration file.
func ConfigPath(e env.Env) string {
	return filepath.Join(e.ConfigHome(), "agnthub", "config.yaml")
}

type configFile struct {
	SkillInstaller *Installer `yaml:"skillInstaller"`
	// APIURL is the registry API origin automatic install reports go to.
	APIURL string `yaml:"apiURL"`
}

// ErrConfig reports an invalid user configuration; nothing has run.
var ErrConfig = errors.New("invalid agnthub configuration")

// readConfig strictly decodes the user configuration file; an absent file is empty.
func readConfig(path string) (configFile, error) {
	var file configFile
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return file, fmt.Errorf("read %s: %w", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return file, fmt.Errorf("%w: %s: %w", ErrConfig, path, err)
	}
	return file, nil
}

// LoadInstaller reads the Skill installer from the user configuration file,
// returning the default when the file or its skillInstaller section is absent.
// Only this user-level file can configure the backend; sources never can.
func LoadInstaller(path string) (Installer, error) {
	file, err := readConfig(path)
	if err != nil {
		return Installer{}, err
	}
	if file.SkillInstaller == nil {
		return DefaultInstaller(), nil
	}
	installer := *file.SkillInstaller
	installer.Origin = path
	if err := installer.Validate(); err != nil {
		return Installer{}, fmt.Errorf("%w: %s: %w", ErrConfig, path, err)
	}
	return installer, nil
}

// LoadAPIURL reads the configured registry API origin, "" when unset.
func LoadAPIURL(path string) (string, error) {
	file, err := readConfig(path)
	return file.APIURL, err
}

// placeholderPattern finds "{name}" tokens; any other brace use stays literal.
var placeholderPattern = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_.-]*\}`)

// Validate rejects a missing executable, missing argument list or unknown placeholders.
func (i Installer) Validate() error {
	if strings.TrimSpace(i.Executable) == "" {
		return errors.New("skillInstaller.executable is required")
	}
	if !filepath.IsAbs(i.Executable) && strings.ContainsAny(i.Executable, `/\`) {
		return fmt.Errorf(
			"skillInstaller.executable %q must be a command name found in PATH or an absolute path",
			i.Executable,
		)
	}
	if i.Args == nil {
		return errors.New("skillInstaller.args is required (an argument array)")
	}
	for _, group := range []struct {
		name string
		args []string
	}{{"args", i.Args}, {"projectArgs", i.ProjectArgs}, {"globalArgs", i.GlobalArgs}} {
		for n, arg := range group.args {
			for _, token := range placeholderPattern.FindAllString(arg, -1) {
				switch token {
				case PlaceholderSource, PlaceholderSkill, PlaceholderTarget:
				default:
					return fmt.Errorf(
						"skillInstaller.%s[%d] uses unknown placeholder %s "+
							"(supported: {source}, {skill}, {target})",
						group.name, n, token,
					)
				}
			}
		}
	}
	return nil
}

// Values are the literal values substituted into one installer invocation.
type Values struct {
	Source string
	Skill  string
	Target string
}

// Command returns the argument vector (without the executable) for one Skill
// installation in scope. Each template stays exactly one argument: values are
// substituted literally and never re-scanned, split or shell-evaluated.
func (i Installer) Command(values Values, scope harness.Scope) []string {
	extra := i.ProjectArgs
	if scope == harness.ScopeGlobal {
		extra = i.GlobalArgs
	}
	args := make([]string, 0, len(i.Args)+len(extra))
	for _, group := range [2][]string{i.Args, extra} {
		for _, template := range group {
			args = append(args, expand(template, values))
		}
	}
	return args
}

func expand(template string, values Values) string {
	return placeholderPattern.ReplaceAllStringFunc(template, func(token string) string {
		switch token {
		case PlaceholderSource:
			return values.Source
		case PlaceholderSkill:
			return values.Skill
		case PlaceholderTarget:
			return values.Target
		default:
			return token
		}
	})
}

// Display renders the executable and arguments for humans; it is never executed.
func Display(executable string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	for _, part := range append([]string{executable}, args...) {
		if part == "" || strings.ContainsAny(part, " \t\n'\"\\$`;&|<>(){}*?[]#~!") {
			part = "'" + strings.ReplaceAll(part, "'", `'\''`) + "'"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}
