package discovery

import (
	"slices"
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/source"
)

// maxLicenseBytes bounds the license file discovery reads; longer texts
// (no common license is) leave the source without one.
const maxLicenseBytes = 64 << 10

// licenseBases and licenseExtensions name root license files, most preferred
// first, compared case-insensitively.
var (
	licenseBases      = []string{"license", "licence", "copying"}
	licenseExtensions = []string{"", ".md", ".txt"}
)

// license reads the source's root license file. It never fails the scan: a
// missing, oversized, non-regular or unreadable file is no license.
func (s *scanner) license() *agent.License {
	entries, err := s.tree.ReadDir(".")
	if err != nil {
		return nil
	}
	best, bestRank := "", -1
	for _, entry := range entries {
		rank := licenseRank(entry.Name)
		if rank < 0 || entry.Kind != source.EntryFile || entry.Size > maxLicenseBytes {
			continue
		}
		if bestRank < 0 || rank < bestRank {
			best, bestRank = entry.Name, rank
		}
	}
	if best == "" {
		return nil
	}
	raw, err := s.read(best)
	if err != nil {
		return nil
	}
	return agent.ParseLicense(best, raw)
}

// licenseRank orders root license file names by preference, or returns -1
// for any other name.
func licenseRank(name string) int {
	lower := strings.ToLower(name)
	for bi, base := range licenseBases {
		rest, ok := strings.CutPrefix(lower, base)
		if !ok {
			continue
		}
		if ei := slices.Index(licenseExtensions, rest); ei >= 0 {
			return bi*len(licenseExtensions) + ei
		}
	}
	return -1
}
