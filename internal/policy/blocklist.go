package policy

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// BlocklistChange edits one Android app's place in the policy: "block" adds
// it to distractions, "always_allow" to essentials, "unlist" removes it from
// both. Websites and Mac apps cannot be enforced yet, so they are not
// accepted here.
type BlocklistChange struct {
	Action         string `json:"action"`
	AndroidPackage string `json:"android_package"`
}

var (
	ErrInvalidBlocklistChange = errors.New("invalid blocklist change")
	androidPackage            = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*(\.[a-zA-Z0-9_]+)+$`)
)

func (c *BlocklistChange) Normalize() error {
	c.Action, c.AndroidPackage = strings.TrimSpace(c.Action), strings.TrimSpace(c.AndroidPackage)
	switch c.Action {
	case "block", "always_allow", "unlist":
	default:
		return fmt.Errorf(`%w: action must be "block", "always_allow" or "unlist"`, ErrInvalidBlocklistChange)
	}
	if strings.Contains(c.AndroidPackage, "/") || strings.HasSuffix(c.AndroidPackage, ".com") && !strings.HasPrefix(c.AndroidPackage, "com.") {
		return fmt.Errorf("%w: %q looks like a website; only Android apps (package names such as com.twitter.android) can be blocked so far", ErrInvalidBlocklistChange, c.AndroidPackage)
	}
	if !androidPackage.MatchString(c.AndroidPackage) {
		return fmt.Errorf("%w: %q is not an Android package name", ErrInvalidBlocklistChange, c.AndroidPackage)
	}
	return nil
}

// ApplyBlocklist returns config with the changes applied, one group per app,
// as the panel's editor does.
func ApplyBlocklist(config Config, changes []BlocklistChange) Config {
	next := config
	for _, change := range changes {
		without := func(groups []TargetGroup) []TargetGroup {
			result := []TargetGroup{}
			for _, group := range groups {
				packages := []string{}
				for _, value := range group.AndroidPackages {
					if value != change.AndroidPackage {
						packages = append(packages, value)
					}
				}
				group.AndroidPackages = packages
				if len(group.AndroidPackages)+len(group.MacApps)+len(group.Domains) > 0 {
					result = append(result, group)
				}
			}
			return result
		}
		next.Distractions, next.Essentials = without(next.Distractions), without(next.Essentials)
		group := TargetGroup{ID: change.AndroidPackage, Label: change.AndroidPackage, AndroidPackages: []string{change.AndroidPackage}}
		switch change.Action {
		case "block":
			next.Distractions = append(next.Distractions, group)
		case "always_allow":
			next.Essentials = append(next.Essentials, group)
		}
	}
	return next
}

// BlocklistState is where pkg stands in config, as the action that would put
// it there: "block", "always_allow" or "unlist".
func BlocklistState(config Config, pkg string) string {
	has := func(groups []TargetGroup) bool {
		for _, group := range groups {
			for _, value := range group.AndroidPackages {
				if value == pkg {
					return true
				}
			}
		}
		return false
	}
	switch {
	case has(config.Distractions):
		return "block"
	case has(config.Essentials):
		return "always_allow"
	}
	return "unlist"
}
