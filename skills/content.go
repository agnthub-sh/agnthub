package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/source"
)

// SkillFile is the file that marks and describes a Skill directory.
const SkillFile = "SKILL.md"

// Reading limits for one Skill directory, source or installed.
const (
	maxSkillMDBytes   int64 = 1 << 20
	maxSkillBytes     int64 = 25 << 20
	maxSkillFiles           = 1000
	maxSkillDepth           = 8
	maxSourceEntries        = 20000
	maxSourceDepth          = 5
	maxLocationSkills       = 2000
)

// Ignored names mirror what skills.sh deliberately leaves out of an installed copy,
// so a faithful copy has the same content digest as its source.
var (
	ignoredFiles = map[string]bool{"metadata.json": true}
	ignoredDirs  = map[string]bool{".git": true, "__pycache__": true, "__pypackages__": true}
)

// Content is the identity, declared version and content digest of one Skill directory.
type Content struct {
	// Name is the SKILL.md frontmatter name.
	Name string
	// Version is metadata.version as declared; HasVersion reports a string value.
	Version    string
	HasVersion bool
	// Digest covers every installable file's relative path and bytes.
	Digest string
}

// parseSkillMD reads the identity and metadata.version of a SKILL.md.
func parseSkillMD(name string, raw []byte) (Content, error) {
	doc, err := agent.ParseMarkdown(name, raw)
	if err != nil {
		return Content{}, err
	}
	content := Content{Name: strings.TrimSpace(doc.String("name"))}
	if content.Name == "" {
		return Content{}, fmt.Errorf("%s: frontmatter has no name", name)
	}
	if metadata, ok := doc.Frontmatter["metadata"].(map[string]any); ok {
		switch version := metadata["version"].(type) {
		case string:
			content.Version, content.HasVersion = version, true
		case nil:
		default:
			// A YAML number such as 1.0 is not a version string.
			content.Version, content.HasVersion = fmt.Sprint(version), true
		}
	}
	return content, nil
}

type fileDigest struct {
	rel    string
	digest [sha256.Size]byte
}

func contentDigest(files []fileDigest) string {
	slices.SortFunc(files, func(a, b fileDigest) int { return strings.Compare(a.rel, b.rel) })
	h := sha256.New()
	for _, file := range files {
		h.Write([]byte(file.rel))
		h.Write([]byte{0})
		h.Write(file.digest[:])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// readSourceSkill reads a Skill directory from an inert source tree. Links,
// submodules and special files are refused because their bytes cannot be verified.
func readSourceSkill(tree source.Tree, dir string) (Content, error) {
	raw, err := tree.ReadFile(path.Join(dir, SkillFile), maxSkillMDBytes)
	if err != nil {
		return Content{}, err
	}
	content, err := parseSkillMD(path.Join(dir, SkillFile), raw)
	if err != nil {
		return Content{}, err
	}
	var files []fileDigest
	var total int64
	var walk func(rel string, depth int) error
	walk = func(rel string, depth int) error {
		if depth > maxSkillDepth {
			return fmt.Errorf("%s: skill directory is nested too deeply", dir)
		}
		entries, err := tree.ReadDir(path.Join(dir, rel))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			child := path.Join(rel, entry.Name)
			switch entry.Kind {
			case source.EntryDir:
				if ignoredDirs[entry.Name] {
					continue
				}
				if err := walk(child, depth+1); err != nil {
					return err
				}
			case source.EntryFile:
				if ignoredFiles[entry.Name] {
					continue
				}
				if len(files) >= maxSkillFiles {
					return fmt.Errorf("%s: skill has more than %d files", dir, maxSkillFiles)
				}
				data, err := tree.ReadFile(path.Join(dir, child), maxSkillBytes-total)
				if err != nil {
					return fmt.Errorf("%s: %w", path.Join(dir, child), err)
				}
				total += int64(len(data))
				files = append(files, fileDigest{rel: child, digest: sha256.Sum256(data)})
			default:
				return fmt.Errorf(
					"%s is a %s; agnthub only verifies regular files in a skill",
					path.Join(dir, child), entry.Kind,
				)
			}
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return Content{}, err
	}
	content.Digest = contentDigest(files)
	return content, nil
}

// errNotSkill reports a directory without a SKILL.md.
var errNotSkill = errors.New("no " + SkillFile)

// readInstalledSkill reads an installed Skill directory. Harnesses load Skills
// through symbolic links (skills.sh links shared copies), so links are followed,
// within the same depth, file and size limits as source Skills.
func readInstalledSkill(dir string) (Content, error) {
	raw, err := readLimited(filepath.Join(dir, SkillFile), maxSkillMDBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return Content{}, errNotSkill
	}
	if err != nil {
		return Content{}, err
	}
	content, err := parseSkillMD(filepath.Join(dir, SkillFile), raw)
	if err != nil {
		return Content{}, err
	}
	var files []fileDigest
	var total int64
	var walk func(rel string, depth int) error
	walk = func(rel string, depth int) error {
		if depth > maxSkillDepth {
			return fmt.Errorf("%s: skill directory is nested too deeply", dir)
		}
		entries, err := os.ReadDir(filepath.Join(dir, rel))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			child := path.Join(rel, entry.Name())
			info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(child)))
			if err != nil {
				return err
			}
			switch {
			case info.IsDir():
				if ignoredDirs[entry.Name()] {
					continue
				}
				if err := walk(child, depth+1); err != nil {
					return err
				}
			case info.Mode().IsRegular():
				if ignoredFiles[entry.Name()] {
					continue
				}
				if len(files) >= maxSkillFiles {
					return fmt.Errorf("%s: skill has more than %d files", dir, maxSkillFiles)
				}
				data, err := readLimited(filepath.Join(dir, filepath.FromSlash(child)),
					maxSkillBytes-total)
				if err != nil {
					return err
				}
				total += int64(len(data))
				files = append(files, fileDigest{rel: child, digest: sha256.Sum256(data)})
			default:
				return fmt.Errorf("%s is not a regular file or directory", child)
			}
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return Content{}, err
	}
	content.Digest = contentDigest(files)
	return content, nil
}

func readLimited(name string, limit int64) ([]byte, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s: %w", name, source.ErrTooLarge)
	}
	return os.ReadFile(name)
}

// skillName returns the frontmatter name of dir's SKILL.md, or "" when unreadable.
func skillName(dir string) string {
	raw, err := readLimited(filepath.Join(dir, SkillFile), maxSkillMDBytes)
	if err != nil {
		return ""
	}
	content, err := parseSkillMD(filepath.Join(dir, SkillFile), raw)
	if err != nil {
		return ""
	}
	return content.Name
}

// findSourceSkill locates the one Skill directory named skill in a source tree:
// a bounded breadth-first walk for SKILL.md files whose frontmatter name matches.
// Several matches are ambiguous rather than resolved by discovery order.
func findSourceSkill(tree source.Tree, skill string) (string, error) {
	type queued struct {
		dir   string
		depth int
	}
	queue := []queued{{dir: ".", depth: 0}}
	examined := 0
	var matches []string
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		entries, err := tree.ReadDir(current.dir)
		if err != nil {
			return "", err
		}
		examined += len(entries)
		if examined > maxSourceEntries {
			return "", fmt.Errorf("source tree exceeds %d entries while looking for skill %s",
				maxSourceEntries, skill)
		}
		for _, entry := range entries {
			child := path.Join(current.dir, entry.Name)
			switch {
			case entry.Kind == source.EntryFile && entry.Name == SkillFile:
				raw, err := tree.ReadFile(child, maxSkillMDBytes)
				if err != nil {
					continue
				}
				if content, err := parseSkillMD(child, raw); err == nil && content.Name == skill {
					matches = append(matches, current.dir)
				}
			case entry.Kind == source.EntryDir && current.depth < maxSourceDepth &&
				!ignoredDirs[entry.Name] && entry.Name != "node_modules":
				queue = append(queue, queued{dir: child, depth: current.depth + 1})
			}
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no SKILL.md named %s", skill)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("skill %s is ambiguous: %s", skill, strings.Join(matches, ", "))
	}
}
