package activity

import (
	"errors"
	"net/url"
	"strings"
)

const (
	LabelKindApp  = "app"
	LabelKindSite = "site"
	Unlabeled     = "unlabeled"
)

// Label names what the owner considers an app or site to be. An app label
// matches a Mac app name or an Android package exactly (ignoring case); a
// site label matches a domain and its subdomains.
type Label struct {
	Kind  string `json:"kind"`
	Match string `json:"match"`
	Label string `json:"label"`
}

// LabelChange is one proposed edit: "set" adds or replaces the label for a
// kind and match; "remove" drops it.
type LabelChange struct {
	Action string `json:"action"`
	Kind   string `json:"kind"`
	Match  string `json:"match"`
	Label  string `json:"label,omitempty"`
}

var ErrInvalidLabel = errors.New("invalid activity label")

// Normalize validates a change and puts its match in canonical form.
func (c *LabelChange) Normalize() error {
	c.Action, c.Kind = strings.TrimSpace(c.Action), strings.TrimSpace(c.Kind)
	c.Match, c.Label = strings.TrimSpace(c.Match), strings.TrimSpace(c.Label)
	if c.Action != "set" && c.Action != "remove" {
		return errors.Join(ErrInvalidLabel, errors.New(`action must be "set" or "remove"`))
	}
	switch c.Kind {
	case LabelKindApp:
	case LabelKindSite:
		c.Match = siteMatch(c.Match)
	default:
		return errors.Join(ErrInvalidLabel, errors.New(`kind must be "app" or "site"`))
	}
	if c.Match == "" || len(c.Match) > 200 {
		return errors.Join(ErrInvalidLabel, errors.New("match must be 1 to 200 characters"))
	}
	if c.Action == "set" && (c.Label == "" || len(c.Label) > 60) {
		return errors.Join(ErrInvalidLabel, errors.New("label must be 1 to 60 characters"))
	}
	if c.Action == "remove" {
		c.Label = ""
	}
	return nil
}

// siteMatch accepts "youtube.com", "www.youtube.com" or a full URL.
func siteMatch(value string) string {
	value = strings.ToLower(value)
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			value = parsed.Hostname()
		}
	}
	return strings.TrimPrefix(strings.Trim(value, "./ "), "www.")
}

// LabelFor returns the owner's label for a segment. A site label wins over
// the browser's app label.
func LabelFor(labels []Label, segment Segment) string {
	best, bestMatch := "", ""
	for _, label := range labels {
		// The most specific site wins: music.youtube.com over youtube.com.
		if label.Kind == LabelKindSite && segment.Domain != "" &&
			(segment.Domain == label.Match || strings.HasSuffix(segment.Domain, "."+label.Match)) && len(label.Match) > len(bestMatch) {
			best, bestMatch = label.Label, label.Match
		}
	}
	if best != "" {
		return best
	}
	for _, label := range labels {
		if label.Kind == LabelKindApp && strings.EqualFold(label.Match, segment.App) {
			return label.Label
		}
	}
	return Unlabeled
}
