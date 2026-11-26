//go:build !darwin && !linux

package desktop

import "time"

// Prompt is not implemented here yet; it reports that nothing was answered.
func Prompt(title, detail string, options []string, timeout time.Duration) int { return -1 }

// PromptSupported reports whether Prompt shows anything here.
func PromptSupported() bool { return false }

// PromptShowing reports whether a prompt is showing.
func PromptShowing() bool { return false }
