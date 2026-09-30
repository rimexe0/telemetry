package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Owner labels that change enforcement. "distraction" is blocked while
// distractions are blocked; "essential" is never blocked. Other labels only
// name time.
const (
	LabelDistraction = "distraction"
	LabelEssential   = "essential"
)

// LabeledTarget is one owner label: kind "app" (an Android package or a Mac
// app name) or "site" (a domain).
type LabeledTarget struct {
	Kind  string
	Match string
	Label string
}

// WithLabels adds one target group per enforcing label to the policy's own
// lists, so a single "x is a distraction" covers every device that can
// enforce it.
func WithLabels(config Config, labels []LabeledTarget) Config {
	next := config
	next.Distractions = append([]TargetGroup(nil), config.Distractions...)
	next.Essentials = append([]TargetGroup(nil), config.Essentials...)
	for _, label := range labels {
		group, ok := labelGroup(label)
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(label.Label)) {
		case LabelDistraction:
			next.Distractions = append(next.Distractions, group)
		case LabelEssential:
			next.Essentials = append(next.Essentials, group)
		}
	}
	return next
}

func labelGroup(label LabeledTarget) (TargetGroup, bool) {
	digest := sha256.Sum256([]byte(label.Kind + "\x00" + strings.ToLower(label.Match)))
	group := TargetGroup{ID: "label-" + hex.EncodeToString(digest[:6]), Label: label.Match}
	switch {
	case label.Kind == "site":
		group.Domains = []string{label.Match}
	case label.Kind == "app" && androidPackage.MatchString(label.Match):
		group.AndroidPackages = []string{label.Match}
	case label.Kind == "app":
		group.MacApps = []string{label.Match}
	default:
		return TargetGroup{}, false
	}
	return group, true
}
