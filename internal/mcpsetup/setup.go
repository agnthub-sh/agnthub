package mcpsetup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

// Runner runs a Harness command in dir with env and returns its combined output.
type Runner func(ctx context.Context, dir string, env, argv []string) ([]byte, error)

// ExecRunner runs commands as child processes.
func ExecRunner(ctx context.Context, dir string, env, argv []string) ([]byte, error) {
	//nolint:gosec // G204: a Harness executable found on PATH with fixed mcp subcommands
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = nil
	return cmd.CombinedOutput()
}

// commandTimeout bounds one Harness command; they start in a second or two.
const commandTimeout = 2 * time.Minute

// Setup scans and changes Harness MCP configurations.
type Setup struct {
	// Ctx resolves configuration paths; ProjectRoot is the project of project
	// scope, LookPath finds the Harnesses' own commands.
	Ctx harness.Context
	// Run runs Harness commands; nil runs them as child processes.
	Run Runner
	Now func() time.Time

	opencodeMajor *int
}

// Target is agnthub's entry in one configuration: a Harness (and omp profile) in
// one scope.
type Target struct {
	Harness agent.HarnessID
	// Profile is the omp profile ("default" or its name); empty for other
	// Harnesses and for omp's project file, which every profile reads.
	Profile string
	Scope   harness.Scope
	// Path is the configuration file that holds, or will hold, the entry.
	Path string
	// State is what Path holds under the agnthub name.
	State State
	// Existing describes a foreign entry (its URL or command).
	Existing string
	// Err is a configuration that cannot be read; nothing is written to it.
	Err error

	spec   *spec
	apiKey string
	// at is the path of the agnthub entry in the file (OpenCode 1 and 2 differ).
	at []string
	// files are every file read for this target (OpenCode merges several).
	files []string
}

// Scan finds agnthub's entry for each Harness in scope. omp gets a target per
// profile: the default profile and every named one, or only those in profiles.
// apiKey is the key an install would write, which makes an entry without it
// outdated.
func (s *Setup) Scan(
	ctx context.Context,
	ids []agent.HarnessID,
	scope harness.Scope,
	profiles []string,
	apiKey string,
) ([]*Target, error) {
	var targets []*Target
	for _, id := range ids {
		sp, ok := specs[id]
		if !ok {
			return nil, fmt.Errorf("unknown harness %q", id)
		}
		if id != agent.OMP || scope == harness.ScopeProject {
			targets = append(targets, s.scanOne(ctx, sp, scope, "", apiKey))
			continue
		}
		names := profiles
		if len(names) == 0 {
			named, err := harness.OMPProfiles(s.Ctx)
			if err != nil {
				return nil, err
			}
			names = append([]string{"default"}, named...)
		}
		for _, name := range names {
			if _, err := harness.MCPUserConfigFiles(s.Ctx, id, name); err != nil {
				return nil, err
			}
			targets = append(targets, s.scanOne(ctx, sp, scope, name, apiKey))
		}
	}
	return targets, nil
}

func (s *Setup) scanOne(
	ctx context.Context,
	sp *spec,
	scope harness.Scope,
	profile, apiKey string,
) *Target {
	t := &Target{Harness: sp.id, Profile: profile, Scope: scope, spec: sp, apiKey: apiKey}
	if scope == harness.ScopeProject {
		for _, rel := range sp.projectFiles {
			t.files = append(t.files, filepath.Join(s.Ctx.ProjectRoot, rel))
		}
	} else {
		files, err := harness.MCPUserConfigFiles(s.Ctx, sp.id, profile)
		if err != nil {
			t.Err = err
			return t
		}
		t.files = files
	}
	t.Path = t.files[0]
	// OpenCode's older config.json is read, never chosen for a new entry.
	writable := t.files
	if sp.id == agent.OpenCode && scope != harness.ScopeProject {
		writable = t.files[:2]
	}
	chosen := ""
	for _, path := range t.files {
		src, _, err := readFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Path, t.Err = path, fmt.Errorf("cannot read %s: %w", path, err)
			return t
		}
		if chosen == "" && slices.Contains(writable, path) {
			chosen = path
		}
		entry, at, err := s.find(ctx, sp, src)
		if err != nil {
			t.Path, t.Err = path, fmt.Errorf("%s cannot be parsed, so agnthub leaves it unchanged "+
				"(fix it and run this again): %w", path, err)
			return t
		}
		if entry != nil {
			t.Path, t.at = path, at
			t.State = sp.classify(entry, apiKey)
			if t.State == Foreign {
				t.Existing = describe(entry)
			}
			return t
		}
	}
	if chosen != "" {
		t.Path = chosen
	}
	t.State = Absent
	return t
}

// find returns agnthub's entry in a configuration and its path there.
func (s *Setup) find(ctx context.Context, sp *spec, src []byte) (map[string]any, []string, error) {
	var data map[string]any
	var err error
	if sp.toml {
		data, err = parseTOML(src)
	} else {
		var doc *jsonDoc
		if doc, err = parseJSON(src); err == nil {
			data, err = doc.data()
		}
	}
	if err != nil {
		return nil, nil, err
	}
	for _, at := range s.entryPaths(ctx, sp, data) {
		if entry, ok := lookup(data, at).(map[string]any); ok {
			return normalize(entry).(map[string]any), at, nil
		}
	}
	return nil, nil, nil
}

// entryPaths are where a Harness reads agnthub's entry, the one to write first.
func (s *Setup) entryPaths(ctx context.Context, sp *spec, data map[string]any) [][]string {
	at := append(slices.Clone(sp.servers), ServerName)
	if sp.id != agent.OpenCode {
		return [][]string{at}
	}
	v1 := []string{"mcp", ServerName}
	if s.opencode2(ctx, data) {
		return [][]string{at, v1}
	}
	return [][]string{v1, at}
}

// opencode2 reports whether to write OpenCode 2's mcp.servers shape: the file
// already uses it, or it has no OpenCode 1 servers and the installed OpenCode is
// 2 or later. OpenCode 2 also reads OpenCode 1's shape, OpenCode 1 not the new
// one, so an unknown version gets the old shape.
func (s *Setup) opencode2(ctx context.Context, data map[string]any) bool {
	if mcp, ok := data["mcp"].(map[string]any); ok {
		if _, ok := mcp["servers"].(map[string]any); ok {
			return true
		}
		for name, server := range mcp {
			if entry, ok := server.(map[string]any); ok && name != ServerName &&
				entry["type"] != nil {
				return false
			}
		}
	}
	return s.opencodeVersion(ctx) >= 2
}

// opencodeVersion is the major version of the opencode on PATH, 0 if unknown.
func (s *Setup) opencodeVersion(ctx context.Context) int {
	if s.opencodeMajor != nil {
		return *s.opencodeMajor
	}
	major := 0
	s.opencodeMajor = &major
	binary, ok := s.lookPath("opencode")
	if !ok {
		return 0
	}
	out, err := s.run(ctx, s.Ctx.Env.Home(), []string{binary, "--version"})
	if err != nil {
		return 0
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
	if i := strings.IndexByte(version, '.'); i > 0 {
		major, _ = strconv.Atoi(version[:i])
	}
	s.opencodeMajor = &major
	return major
}

func lookup(data map[string]any, path []string) any {
	var cur any = data
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[key]
	}
	return cur
}

// Action is what an install or uninstall did for one target.
type Action string

// Actions.
const (
	ActionInstall   Action = "install"
	ActionUpdate    Action = "update"
	ActionUnchanged Action = "unchanged"
	ActionRemove    Action = "remove"
	// ActionSkip: left alone (an agnthub entry agnthub did not write).
	ActionSkip Action = "skip"
	// ActionFail: the configuration could not be changed; it was left as it was.
	ActionFail Action = "fail"
)

// Result is the outcome for one target.
type Result struct {
	Action Action
	// Reason explains a skip or failure.
	Reason string
	// Backup is the copy of the previous file, when it was changed.
	Backup string
	// Command is the Harness command that made the change, if one did.
	Command []string
	// Mechanism is "command" (the Harness's own mcp command) or "file".
	Mechanism string
}

// Install adds the hosted server to t, or replaces an older agnthub entry. A
// foreign agnthub entry is replaced only with force.
func (s *Setup) Install(ctx context.Context, t *Target, force bool) Result {
	switch {
	case t.Err != nil:
		return Result{Action: ActionFail, Reason: t.Err.Error()}
	case t.State == Installed:
		return Result{Action: ActionUnchanged}
	case t.State == Foreign && !force:
		return Result{Action: ActionSkip, Reason: "its agnthub server was not set up by agnthub (" +
			t.Existing + "); rerun with --force to replace it"}
	}
	action := ActionInstall
	if t.State != Absent {
		action = ActionUpdate
	}
	if binary, ok := s.command(t, t.apiKey == ""); ok {
		res := s.viaCommand(ctx, t, binary, true)
		if res.Action != ActionFail {
			res.Action = action
		}
		return res
	}
	res := s.viaFile(ctx, t, true)
	if res.Action != ActionFail {
		res.Action = action
	}
	return res
}

// Uninstall removes agnthub's entry from t when agnthub wrote it.
func (s *Setup) Uninstall(ctx context.Context, t *Target) Result {
	switch {
	case t.Err != nil:
		return Result{Action: ActionFail, Reason: t.Err.Error()}
	case !t.State.Ours():
		return Result{Action: ActionSkip, Reason: "no agnthub server set up by agnthub"}
	}
	if binary, ok := s.command(t, true); ok {
		res := s.viaCommand(ctx, t, binary, false)
		if res.Action != ActionFail {
			res.Action = ActionRemove
		}
		return res
	}
	res := s.viaFile(ctx, t, false)
	if res.Action != ActionFail {
		res.Action = ActionRemove
	}
	return res
}

// command is the Harness's own command for t, when it can do the change: never
// with a secret, which would show on its command line.
func (s *Setup) command(t *Target, allowed bool) (string, bool) {
	c := t.spec.cli
	if !allowed || c == nil || (t.Scope == harness.ScopeProject && !c.project) {
		return "", false
	}
	return s.lookPath(c.binary)
}

func (s *Setup) lookPath(name string) (string, bool) {
	if s.Ctx.LookPath == nil {
		return "", false
	}
	path, err := s.Ctx.LookPath(name)
	return path, err == nil
}

func (s *Setup) run(ctx context.Context, dir string, argv []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	run := s.Run
	if run == nil {
		run = ExecRunner
	}
	return run(ctx, dir, s.Ctx.Env.Pairs(), argv)
}

// viaCommand runs the Harness's mcp command (remove first when an entry is
// there), then reads the file back. On failure the backup is restored.
func (s *Setup) viaCommand(ctx context.Context, t *Target, binary string, install bool) Result {
	res := Result{Mechanism: "command"}
	backup, err := s.backup(t)
	if err != nil {
		return fail(res, err)
	}
	res.Backup = backup
	dir := s.Ctx.Env.Home()
	if t.Scope == harness.ScopeProject {
		dir = s.Ctx.ProjectRoot
	}
	var steps [][]string
	if t.State != Absent {
		steps = append(steps, append([]string{binary}, t.spec.cli.remove(t.Scope)...))
	}
	if install {
		steps = append(steps, append([]string{binary}, t.spec.cli.add(t.Scope)...))
	}
	for _, argv := range steps {
		res.Command = append([]string{t.spec.cli.binary}, argv[1:]...)
		out, err := s.run(ctx, dir, argv)
		if err != nil {
			s.restore(t, backup)
			return fail(res, fmt.Errorf("`%s` failed: %w: %s", strings.Join(res.Command, " "), err,
				strings.TrimSpace(string(out))))
		}
	}
	want := Absent
	if install {
		want = Installed
	}
	after := s.scanOne(ctx, t.spec, t.Scope, t.Profile, t.apiKey)
	if after.Err != nil || after.State != want {
		s.restore(t, backup)
		got := string(after.State)
		if after.Err != nil {
			got = after.Err.Error()
		}
		return fail(
			res,
			fmt.Errorf("`%s` succeeded, but %s then held %s instead of the expected entry; "+
				"the previous file was restored", strings.Join(res.Command, " "), t.Path, got),
		)
	}
	return res
}

// viaFile edits the configuration file: only the agnthub entry changes.
func (s *Setup) viaFile(ctx context.Context, t *Target, install bool) Result {
	res := Result{Mechanism: "file"}
	src, mode, err := readFile(t.Path)
	existed := err == nil
	switch {
	case errors.Is(err, fs.ErrNotExist):
		src, mode = nil, 0o600
	case err != nil:
		return fail(res, err)
	}
	var out []byte
	if t.spec.toml {
		out, err = s.editTOML(t, src, install)
	} else {
		out, err = s.editJSON(ctx, t, src, install)
	}
	if err != nil {
		return fail(res, fmt.Errorf("%s: %w", t.Path, err))
	}
	if existed {
		if res.Backup, err = s.backup(t); err != nil {
			return fail(res, err)
		}
	}
	dirMode := os.FileMode(0o700)
	if t.Scope == harness.ScopeProject {
		dirMode = 0o750
	}
	if err := writeAtomic(t.Path, out, mode, dirMode); err != nil {
		return fail(res, err)
	}
	return res
}

func (s *Setup) editJSON(ctx context.Context, t *Target, src []byte, install bool) ([]byte, error) {
	doc, err := parseJSON(src)
	if err != nil {
		return nil, err
	}
	before, err := doc.data()
	if err != nil {
		return nil, err
	}
	paths := s.entryPaths(ctx, t.spec, before)
	var removed [][]string
	for _, at := range paths {
		if !install || !slices.Equal(at, paths[0]) {
			if lookup(before, at) == nil {
				continue
			}
			if _, err := doc.remove(at); err != nil {
				return nil, err
			}
			removed = append(removed, at)
		}
	}
	var want any
	if install {
		entry := t.spec.entry(t.apiKey)
		if err := doc.set(paths[0], entry); err != nil {
			return nil, err
		}
		want = generic(entry)
	}
	out := doc.bytes()
	after, err := parseJSON(out)
	if err != nil {
		return nil, fmt.Errorf("the edited file does not parse (%w); nothing was written", err)
	}
	afterData, err := after.data()
	if err != nil {
		return nil, err
	}
	if err := verify(before, afterData, removed, paths[0], want); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Setup) editTOML(t *Target, src []byte, install bool) ([]byte, error) {
	before, err := parseTOML(src)
	if err != nil {
		return nil, err
	}
	at := append(slices.Clone(t.spec.servers), ServerName)
	exists := lookup(before, at) != nil
	var out []byte
	var want any
	switch {
	case install:
		entry := t.spec.entry(t.apiKey)
		block, err := tomlTable(ServerName, entry)
		if err != nil {
			return nil, err
		}
		if out, err = tomlSet(src, ServerName, block, exists); err != nil {
			return nil, err
		}
		want = generic(entry)
	case exists:
		if out, err = tomlRemove(src, ServerName); err != nil {
			return nil, err
		}
	default:
		return src, nil
	}
	after, err := parseTOML(out)
	if err != nil {
		return nil, fmt.Errorf("the edited file does not parse (%w); nothing was written", err)
	}
	var removed [][]string
	if exists {
		removed = [][]string{at}
	}
	if err := verify(normalize(before).(map[string]any), normalize(after).(map[string]any),
		removed, at, want); err != nil {
		return nil, err
	}
	return out, nil
}

// verify checks that an edit changed nothing but agnthub's entry: removed paths
// are gone, set holds want (nil: gone too), and everything else is equal.
func verify(before, after map[string]any, removed [][]string, set []string, want any) error {
	if want != nil && !reflect.DeepEqual(lookup(after, set), want) {
		return errors.New("the edit did not produce the agnthub entry; nothing was written")
	}
	b, a := deepCopy(before).(map[string]any), deepCopy(after).(map[string]any)
	for _, at := range append(slices.Clone(removed), set) {
		deletePath(b, at)
		deletePath(a, at)
		// A parent left empty is the same as none: the edit may create or drop it.
		for i := len(at) - 1; i > 0; i-- {
			for _, data := range []map[string]any{a, b} {
				if m, ok := lookup(data, at[:i]).(map[string]any); ok && len(m) == 0 {
					deletePath(data, at[:i])
				}
			}
		}
	}
	if !reflect.DeepEqual(a, b) {
		return errors.New("the edit would change more than the agnthub entry; nothing was written")
	}
	return nil
}

func deletePath(data map[string]any, path []string) {
	if m, ok := lookup(data, path[:len(path)-1]).(map[string]any); ok {
		delete(m, path[len(path)-1])
	}
}

func deepCopy(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, inner := range v {
			out[k] = deepCopy(inner)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, inner := range v {
			out[i] = deepCopy(inner)
		}
		return out
	default:
		return v
	}
}

func fail(res Result, err error) Result {
	res.Action = ActionFail
	res.Reason = err.Error()
	return res
}

// readFile reads a configuration file through symbolic links, with its mode.
func readFile(path string) ([]byte, os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s is not a regular file", path)
	}
	src, err := os.ReadFile(path)
	return src, info.Mode().Perm(), err
}

// backupDir keeps copies of the configuration files agnthub changed.
func (s *Setup) backupDir() string {
	return filepath.Join(s.Ctx.Env.StateHome(), "agnthub", "mcp-backups")
}

// backup copies t's file, if it exists, into the backup directory with its mode
// and returns the copy's path.
func (s *Setup) backup(t *Target) (string, error) {
	src, mode, err := readFile(t.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	name := now().UTC().Format("20060102T150405Z") + "-" + string(t.Harness)
	if t.Profile != "" {
		name += "-" + t.Profile
	}
	name += "-" + filepath.Base(t.Path)
	path := filepath.Join(dir, name)
	for i := 2; ; i++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, fs.ErrExist) {
			path = filepath.Join(dir, name+"."+strconv.Itoa(i))
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(src)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return "", werr
		}
		return path, os.Chmod(path, mode)
	}
}

// restore puts a backup back after a Harness command failed.
func (s *Setup) restore(t *Target, backup string) {
	if backup == "" {
		return
	}
	src, mode, err := readFile(backup)
	if err != nil {
		return
	}
	_ = writeAtomic(t.Path, src, mode, 0o700)
}

// writeAtomic replaces path (the file a symbolic link points to) with data in
// mode, through a temporary file in the same directory.
func writeAtomic(path string, data []byte, mode, dirMode os.FileMode) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".agnthub-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, mode)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}
