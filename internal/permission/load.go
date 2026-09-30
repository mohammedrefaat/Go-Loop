package permission

import (
	"fmt"
	"sort"
	"strings"
)

// LoadResult reports what happened when a config's rules were turned into an
// engine. Invalid rules are reported rather than dropped silently: a rule the
// user wrote that never fires is worse than one that failed loudly, because
// they will believe it is protecting something.
type LoadResult struct {
	Rules []Rule
	// Errors holds one message per rule that failed to parse, keyed by the
	// rule text so the caller can show the user exactly what was ignored.
	Errors map[string]error
	// Mode is the resolved mode. It is never empty.
	Mode Mode
}

// Load builds an engine from a config-shaped rule list.
//
// Every rule is attempted: one bad rule does not discard the others, because
// dropping a whole file over a typo leaves the user with no permissions at
// all and no obvious cause. The valid rules are still enforced, and the
// failures are reported for /doctor and a startup notice.
//
// Empty input yields an engine in ModeAuto with no rules, which is exactly
// pi-go's pre-permission behaviour.
func Load(mode string, ruleTexts []string) (*Engine, LoadResult) {
	res := LoadResult{
		Rules:  make([]Rule, 0, len(ruleTexts)),
		Errors: make(map[string]error),
		Mode:   resolveMode(mode),
	}
	for _, text := range ruleTexts {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			// A blank line and a comment are both ways of writing nothing.
			// Treating them as errors would make a commented config look
			// broken.
			continue
		}
		rule, err := ParseRule(trimmed)
		if err != nil {
			res.Errors[trimmed] = err
			continue
		}
		res.Rules = append(res.Rules, rule)
	}
	return New(res.Rules, WithMode(res.Mode)), res
}

// resolveMode maps a config string to a Mode, falling back to ModeAuto. An
// unrecognised mode must not leave the agent unable to act, and it must not
// silently become something more restrictive than the user asked for.
func resolveMode(mode string) Mode {
	m := Mode(strings.TrimSpace(mode))
	if m == "" {
		return ModeAuto
	}
	if !validMode(m) {
		return ModeAuto
	}
	return m
}

// DescribeErrors renders the parse failures as one line each, suitable for a
// system notice. It returns "" when everything parsed.
func (r LoadResult) DescribeErrors() string {
	if len(r.Errors) == 0 {
		return ""
	}
	msgs := make([]string, 0, len(r.Errors))
	for text, err := range r.Errors {
		msgs = append(msgs, fmt.Sprintf("%s: %v", text, err))
	}
	// Map iteration order is random; sort the text into a stable order so
	// the same bad config always produces the same message.
	sort.Strings(msgs)
	return strings.Join(msgs, "; ")
}
