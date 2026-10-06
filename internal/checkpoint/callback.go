package checkpoint

import (
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	llmagent "google.golang.org/adk/v2/agent/llmagent"
)

// snapshotTools are the tools whose effects a code restore can undo. It is
// deliberately just the two that go through the file tool surface: bash can
// change any file in the tree, and observing it is not possible from here, so
// including it would produce snapshots that are correct only by coincidence.
var snapshotTools = map[string]bool{
	"write": true,
	"edit":  true,
}

// BuildBeforeToolCallback returns a callback that snapshots a file's contents
// before a tool call can change them.
//
// Before, not after: the callback observes the path and the old bytes at once,
// and once write returns the old bytes are gone. Snapshotting in
// AfterToolCallback — the seam most observers in this repo use — would save the
// content the agent just produced, which restores to no change at all.
//
// The callback never fails a tool call. A checkpoint that cannot be written is
// a missing feature for that one turn; failing the write would be the agent
// losing the ability to edit a file because an undo history is full.
func BuildBeforeToolCallback(m *Manager, sessionDir func() string) llmagent.BeforeToolCallback {
	return func(_ agent.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		if m == nil || t == nil || !snapshotTools[t.Name()] {
			return nil, nil
		}
		dir := sessionDir()
		if dir == "" {
			return nil, nil
		}
		path, ok := args["file_path"].(string)
		if !ok || path == "" {
			return nil, nil
		}
		// Best-effort: Capture's own errors are logged by the caller, not
		// surfaced to the turn.
		_ = m.Capture(dir, path)
		return nil, nil
	}
}