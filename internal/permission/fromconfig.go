package permission

import "github.com/dimetron/pi-go/internal/config"

// FromConfig builds an engine from a config's permissions section.
//
// It exists so that no surface has to remember the two things that are easy to
// get wrong when wiring this up: a nil section is not an error, and an invalid
// rule must not take the valid ones down with it. Both are handled the same way
// everywhere, which is the point — four call sites each doing this by hand is
// four chances for one of them to be the surface that quietly skips a deny rule.
//
// A nil section yields an engine in ModeAuto with no rules, which is exactly
// pi-go's pre-permission behaviour. That is what keeps a config file written
// before this feature existed working unchanged.
func FromConfig(cfg *config.PermissionConfig, opts ...Option) (*Engine, LoadResult) {
	if cfg == nil {
		return Load("", nil)
	}
	engine, res := Load(cfg.Mode, cfg.Rules)
	// Options supplied by the caller — the approver, the non-interactive policy
	// — are applied on top of the loaded mode, so a surface can be
	// non-interactive while the user has asked for a specific mode.
	for _, opt := range opts {
		opt(engine)
	}
	return engine, res
}

// SetNonInteractive applies the non-interactive policy to an engine built by
// FromConfig. It is the same option WithNonInteractive installs; the method
// exists so a caller holding an engine does not have to hold on to the options
// it was constructed with.
func (e *Engine) SetNonInteractive(policy Decision) {
	WithNonInteractive(policy)(e)
}
