package tools

import "regexp"

// secretPatterns matches common API key/secret assignment patterns.
// Each pattern captures a prefix (key name + delimiter) and the secret value.
var secretPatterns = []*regexp.Regexp{
	// KEY=value or KEY="value" or KEY='value' (shell exports, env files)
	regexp.MustCompile(`(?i)((?:api[_-]?key|secret[_-]?key|access[_-]?key|auth[_-]?token|bearer[_-]?token|private[_-]?key|client[_-]?secret|password|passwd|token|secret)\s*=\s*"?)([^"'\s]{8,})("?)`),
	// export KEY=value
	regexp.MustCompile(`(?i)(export\s+\w*(?:key|token|secret|password|passwd)\w*\s*=\s*"?)([^"'\s]{8,})("?)`),
	// Bearer or Basic <token> in Authorization headers or output
	regexp.MustCompile(`(?i)(Authorization:\s*(?:Bearer|Basic)\s+)(\S{8,})`),
	regexp.MustCompile(`(?i)(Bearer\s+)(\S{8,})`),
	// Vendor-specific API keys
	regexp.MustCompile(`()(sk-ant-[a-zA-Z0-9_-]{20,})`),
	regexp.MustCompile(`()(sk-or-[a-zA-Z0-9_-]{20,})`),
	regexp.MustCompile(`()(sk-[a-zA-Z0-9]{20,})`),
	regexp.MustCompile(`()(xai-[a-zA-Z0-9]{20,})`),
	// ghp_... / gho_... / glpat_... (GitHub/GitLab PATs)
	regexp.MustCompile(`()(ghp_[a-zA-Z0-9]{20,})`),
	regexp.MustCompile(`()(gho_[a-zA-Z0-9]{20,})`),
	regexp.MustCompile(`()(glpat-[a-zA-Z0-9_-]{20,})`),
	// Slack tokens
	regexp.MustCompile(`()(xox[baprs]-[a-zA-Z0-9_-]{10,})`),
	// JWTs
	regexp.MustCompile(`()(eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9._-]+\.[a-zA-Z0-9._-]+)`),
	// AWS access key IDs
	regexp.MustCompile(`()(AKIA[0-9A-Z]{16})`),
	// Private Key Blocks
	regexp.MustCompile(`(?s)(-----BEGIN [A-Z ]+ PRIVATE KEY-----).*?(-----END [A-Z ]+ PRIVATE KEY-----)`),
	// Connection strings with passwords
	regexp.MustCompile(`(?i)((?:postgres|postgresql|mysql|mongodb|redis)://[^:]+:)([^@]+)(@)`),
}

// redactSecrets replaces known API key/secret values with "***" in the given string.
func redactSecrets(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "${1}***${3}")
	}
	return s
}
