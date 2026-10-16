package config

import (
	"path"
	"regexp"
	"strings"
)

// BranchMatches reports whether branch passes the repository gate regex and
// matches one entry in Branches (exact name or path.Match glob).
func (r Repository) BranchMatches(branch string) bool {
	pattern := r.BranchRegex
	if pattern == "" {
		pattern = ".*"
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	if !re.MatchString(branch) {
		return false
	}
	for _, b := range r.Branches {
		if b == branch {
			return true
		}
		if strings.ContainsAny(b, "*?[") {
			if ok, _ := path.Match(b, branch); ok {
				return true
			}
		}
	}
	return false
}
