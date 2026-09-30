# pi-go vs Claude Code — Feature Comparison & Port Plan

> **Scope:** Side-by-side comparison between `pi-go` (the Go coding agent in this repo) and Claude Code,
> producing a ranked port plan for the capabilities Claude Code has and pi-go does not.
>
> **Date:** 2026-09-29
> **Methodology:** Two parallel `explore` agents produced file:line-anchored inventories of both surfaces
> (pi-go: `internal/` 49 packages, 30+ slash commands, 11 CLI subcommands, 4 extension surfaces; Claude Code:
> `code.claude.com/docs`, ~14 pages). This document synthesises them and adds a gap analysis, a proposed
> architecture for the one structural gap, and an ordered delivery plan.
>
> **Relationship to `003-grok-build-review/FEATURE_COMPARISON.md`:** that document compares pi-go against
> grok-build. This one compares against Claude Code and is *narrower in scope* — see `## Not porting`.
>
> **Status:** Proposed — awaiting user approval.

---

## Executive Summary

1. **Most of Claude Code's surface is already in pi-go, and pi-go is ahead in 11 areas Claude Code has no
   equivalent of.** Porting "everything" would be net-negative work. The genuine gaps are the 12 items below,
   worth roughly 2–4 weeks of focused effort, not a rewrite.
2. **There is exactly one structural gap: pi-go has no permission system.** The agent auto-approves every tool
   call. Destructive-operation rules (`git reset --hard`, `git push --force`) are *advisory text in the system
   prompt* (`internal/agent/agent.go:167-168`) — the model is asked to confirm, but nothing enforces it. Every
   other gap is additive.
3. **The single most important design decision is the approval seam.** Every workstream, blocking hooks, plan
   mode, and `/doctor` all need to ask "may this tool run?" exactly once, in one place. Getting that seam wrong
   invalidates the rest of the plan. `## The approval seam` proposes it.
4. **Auto-approve stays the default.** Per the user's decision, pi-go ships the full permission system with
   `auto` as the default mode, so existing behaviour is unchanged and prompting is opt-in.
5. **~40 Claude Code commands are Anthropic product integration and are deliberately not ported** — artifacts,
   design, cloud sessions, marketplace, telemetry. See `## Not porting`.

---

## Where pi-go is already ahead

Porting these would be a regression. Listed so the plan does not touch them.

| Capability | pi-go | Claude Code |
|---|---|---|
| Git-native tools | `git_overview`, `git_file_diff`, `git_hunk` (structured, not bash) | none — uses `Bash(git …)` |
| Declarative workflow engine | `internal/sop` — YAML workflows compiled to ADK graphs, user-overridable at `.pi-go/sops/` | `/pr-comments` etc., no equivalent |
| Memory Palace | `internal/palace` — SQLite drawers, 4-layer contextual memory, temporal KG, semantic embeddings, full `pi memory` CLI | auto-memory: one `MEMORY.md` per project |
| Agent-to-agent | A2A server *and* client (`internal/a2a`, `pi a2a`) | none |
| Editor integration | ACP — `pi acp-server` runs as a Zed agent | IDE extensions (VS Code/JetBrains) |
| Skill supply-chain audit | `internal/audit` — hidden Unicode, BiDi override, "Glassworm"; `pi audit --strip`; skills blocked on critical findings | none |
| Build provenance | `pi verify` — Sigstore SLSA + SBOM verification | none |
| Diagram rendering | Mermaid fences → styled ASCII in-chat (`internal/mermaid`) | none |
| Browser terminal | `pi serve` — xterm.js + PTY pool, pairing-code auth, voice relay | none |
| Token governance | `internal/guardrail` daily caps, `internal/ratelimit` pacing, Prometheus `--metrics` | `/cost`, `/usage` (display only) |
| Subagent breadth | 18 bundled agents; single/parallel/**chain** modes; worktree isolation per agent | many built-ins, one spawn mode |

---

## Not porting

Deliberately excluded. Porting any of these into a local-first, multi-provider, open-source agent would be
meaningless or actively wrong.

| Excluded | Reason |
|---|---|
| `/artifacts`, `/design`, `/design-sync`, `/slides`, `/ppts` | Anthropic-hosted artifact product |
| `/teleport`, `/remote-control`, `/desktop`, `/mobile` | Anthropic cloud sessions |
| `/schedule`, cloud routines | Anthropic cloud scheduling |
| `/usage-credits`, `/privacy-settings`, `/privacy`, `/usage`, `/cost` (billed-plan views) | Tied to Anthropic subscription |
| `/radio`, `/stickers`, `/imagine` | xAI/media product integration |
| `/setup-bedrock`, `/setup-vertex`, `/heapdump` | Anthropic-specific infra |
| `/install-github-app`, `/login` (Claude account) | Anthropic account system |
| Plugin **marketplace** + `strictPluginOnlyCustomization` | Ecosystem, not capability. pi-go's skills+hooks+MCP+SOPs already cover the customization surface |
| `managed-settings.json` / MDM / `claude gateway` / LLM-gateway enterprise tier | Single-user OSS tool; no managed fleet to serve |
| `claude plugin eval` harness | pi-go has `internal/eval` with trajectory metrics + LLM judge — already better suited |
| `ultracode`, `subagentStatusLine`, `statsig` feature flags | Anthropic-internal |
| Chrome integration, Slack integration | Product integrations |

**Kept from Claude Code despite being product-shaped:** `/code-review`, `/security-review`, `/simplify` —
pi-go already has `internal/review` and `code-reviewer`/`codex-review` subagents, so these are mostly a UI
affordance over existing capability.

---

## The twelve gaps

| # | Gap | Effort | Risk | Depends on |
|---|---|---|---|---|
| 1 | Permission system (rules, modes, approval seam) | L | Medium | — |
| 2 | Hooks overhaul (full event set, blocking `PreToolUse`) | M | Low | 1 |
| 3 | `keybindings.json` — rebindable, context-scoped, chords | S–M | Low | — |
| 4 | Checkpointing + `/rewind` | M–L | Medium | — |
| 5 | `/import claude` — pull Claude Code config in | S | Low | — |
| 6 | `@` file mentions + `!` shell mode | S–M | Low | 1 |
| 7 | `/doctor` diagnostics | S | Low | 1, 3 |
| 8 | Skills `paths` gating | S | Low | — |
| 9 | Output styles as instruction sets | S–M | Low | — |
| 10 | Background sessions + tasks pane | M | Medium | 1 |
| 11 | Vim/editor input mode | M | Low | 3 |
| 12 | Headless budgets (`--max-turns`, `--max-budget-usd`, stream-json) | S | Low | — |

(12 rows, 9 workstreams — 1 and 2 share a seam, 3 and 11 share a keymap.)

### Gap 1 — Permissions (the structural one)

**Today:** no approval callback anywhere in the tool path. Confirmed from four angles — `internal/tools/`
has no permission hook, `internal/tui/agent_loop.go` and `internal/agent/agent.go` have no approval path,
`internal/acp/permissions.go` auto-approves deliberately, and the only user-facing confirmations in the TUI
are `/commit` and `/skill-create`.

**Proposed:** `internal/permission` package owning rule parsing and evaluation.

Rule syntax follows Claude Code because it is well-specified and because pi-go already accepts Claude-format
MCP JSON, so the same argument applies:

- `Tool` or `Tool(specifier)`. `Bash`, `Bash(npm run build)`, `Read(./.env)`, `WebFetch(domain:example.com)`
- `*` wildcard including spaces; `:*` ≡ trailing `*`; `Bash(ls *)` matches `ls -la` but not `lsof`
- Tool-name globs (deny/ask only): `"*"`, `"mcp__*"`
- File rules: gitignore-style path patterns
- **Evaluation order: deny → ask → allow. First match in that order wins.** A broad deny beats a narrower
  allow — an allow cannot carve an exception out of a deny.
- Bare-name deny (`Bash`) removes the tool from context; scoped deny (`Bash(rm *)`) keeps it available.

**Modes:** `default` (manual), `acceptEdits`, `plan`, `dontAsk`, `auto`, `bypassPermissions`. **`auto` is
pi-go's default**, so existing sessions behave exactly as they do today. `Shift+Tab` cycles modes; the
session sidebar shows the current mode.

**The change that needs care:** `internal/agent/agent.go:167-168` puts destructive-operation rules in the
system prompt. Once enforcement exists, that text double-prompts — the model asks, then the enforcer asks.
**Delete it in the same commit that enables enforcement.**

### The approval seam

The load-bearing decision. The whole plan needs "may this tool run?" answered once, in one place.

```
PreToolUse hook (may block)  →  permission rules (deny/ask/allow)  →  execute
                                    ↓
                     Ask → TUI prompt │ non-TTY → headless policy
```

- **One call site** in the tool layer. Everything else — hooks, plan mode, `/doctor`, `@`-mention path,
  background sessions — reuses the same decision.
- **Headless never blocks.** ACP, A2A, `--mode print|json`, subagents, and CI have no TTY. `Ask` resolves to a
  configured headless policy there, not a hang. Default: **deny and report** (the user's decision, agreeing
  with the default headless policy proposed in review).
- **`PreToolUse` and permissions are one seam, not two.** That is why hooks are not an independent workstream.
- Rule changes take effect **without restart** (matches Claude Code; the seam is consulted per call).
- Decisions surface via the existing `SystemNoticeCh` / session-logger path — never `fmt.Print` (TUI output
  safety, `CLAUDE.md`).

### Gap 2 — Hooks

Today: shell commands, JSON on stdin, wired to a few ADK callbacks; non-blocking. Target: the full event set
including blocking `PreToolUse` (decisions: `allow`/`deny`/`ask`/`defer`), plus `PostToolUse`, `UserPromptSubmit`,
`SessionStart`/`End`, `PreCompact`, `Stop`/`SubagentStop`, `Notification`, `PostToolUseFailure`, `TaskCompleted`,
`StopFailure`, `WorktreeCreate`/`WorktreeRemove`, `EndConversation`. Hook output becomes structured decisions at
the seam rather than log lines. The `WorktreeCreate`/`Remove` pair is a natural fit — pi-go already manages
worktrees for subagents (`internal/subagent/worktree.go`).

### Gap 3 — Keybindings

Today: hardcoded in `internal/tui/tui.go:1158-1412`. Target: `~/.pi-go/keybindings.json`, auto-reloaded without
restart, with `{context, key, action}` bindings, `action: null` to unbind, space-separated chords
(`"Ctrl+X Ctrl+K"`), and warnings (not errors) for unknown actions — an unknown action is skipped and the
default kept. pi-go's contexts: `chat`, `transcript`, `settings`, `overlay`. Text-editing keys (readline
`Ctrl+A`/`E`/`K`/`U`/`W`) stay non-remappable, matching Claude Code.

### Gap 4 — Checkpointing + `/rewind`

Every prompt that starts a turn writes a checkpoint; keep the 100 most recent per session; swept after
`cleanupPeriodDays`. `/rewind` (or `Esc Esc` with empty input) lists checkpoints with: restore code +
conversation / restore conversation / restore code / summarize from here / summarize up to here. Code restore
is **file snapshots** of files the agent itself wrote — with the documented limits carried over: bash-modified
files and background-subagent edits are not restored; symlinks skipped with a warning naming the count. This
is a new subsystem (`internal/checkpoint`), independent of permissions.

### Gap 5 — `/import claude`

Read `~/.claude/settings.json` permission rules, `keybindings.json`, `CLAUDE.md` → pi-go equivalents, and
convert `.mcp.json`. pi-go already parses Claude-format MCP JSON, so this is mostly translation. Output is a
**preview + confirm** — never silently overwrite pi-go config.

### Gap 6 — `@` file mentions + `!` shell mode

`@path` inserts a file reference with line-range support; `!cmd` runs a shell command and inserts its output.
`@` is the one that touches the seam: mentioning a file outside the sandbox root should go through the
permission/deny path rather than being a special case.

### Gap 7 — `/doctor`

Compose existing checks rather than inventing new ones: `pi ping` (provider connectivity), `pi audit` (skill
supply-chain), `pi session-stats` (session anomalies), config parse validation, PATH/tooling check, and — once
gaps 1 and 3 land — dangling permission rules and invalid keybinding actions. pi-go has the pieces; it lacks
the entry point.

### Gap 8 — Skills `paths` gating

Skill frontmatter gains `paths: ["internal/tui/**"]` — the skill is injected only when the session touches a
matching file. Reduces context cost, which is pi-go's existing concern (`/rtk`, guardrail, autocompact).

### Gap 9 — Output styles

Markdown files in `~/.pi-go/output-styles/` that replace the default system prompt. pi-go's `Roles`
(`--smol`/`--slow`/`--plan`) already occupy adjacent ground; output styles are per-session, roles are
per-invocation. They should compose, not collide — **decide which wins if both are set** (proposal: explicit
`--output-style` beats the role).

### Gaps 10–12 — Secondary

Background sessions + tasks pane (`Ctrl+B`); vim/editor input mode; headless budgets `--max-turns`,
`--max-budget-usd`, and `--output-format stream-json` for CI integration.

---

## Delivery plan

Ordered so each phase is independently mergeable and reviewable. Do not run phases in parallel — 2 depends
on the seam from 1, and 3/7 depend on both.

| Phase | Work | Rationale |
|---|---|---|
| **0** | `/import claude` (5), skills `paths` (8), output styles (9) | Pure additions, no seam needed, no risk. Ships value immediately |
| **1** | **Permissions** (1) + **hooks** (2) together | Same seam. Splitting them means building the seam twice |
| **2** | Keybindings (3) + `/doctor` (7) + headless budgets (12) | Build on the seam; `/doctor` needs it to report dangling rules |
| **3** | Checkpointing + `/rewind` (4) | New subsystem, independent — can run in parallel with 1–2 if capacity allows |
| **4** | `@`-mentions + `!` shell (6), background sessions (10), vim mode (11) | Interaction layer, once enforcement is trustworthy |

**Phase 1 is the one to get right.** It is the only phase that changes how pi-go makes decisions.

### Compatibility choices

- **Accept Claude-format permission rules and MCP JSON natively** — pi-go already accepts Claude-format MCP
  config, so the marginal cost is near zero and `/import claude` becomes cheap.
- **Do not** contort the rest of the design around Claude Code's file layout. pi-go's `~/.pi-go/`, roles,
  SOPs, and Palace are deliberate design, and they stay.

### Cross-cutting

- `make test` / `make lint` / `make vet` green before every push; all commits signed with `-s -S`.
- TUI output safety: new prompts and menus use the session logger / `SystemNoticeCh`, never `fmt.Print`.
- Every gap gets tests in the existing style — permission evaluation is pure and table-testable; keybinding
  parsing likewise; checkpointing needs a real-filesystem test.

---

## Open questions

1. **Phase 1 scope** — permissions + hooks in one phase is a large review. Acceptable, or split permissions
   into "rules + seam" and "hooks on top"?
2. **`/plan` naming** — Claude Code's `plan` is a read-only permission mode. pi-go's `/plan` is the PDD
   workflow. Proposal: keep `/plan` for PDD, name the mode toggle `/permission-mode`. Needs a decision.
3. **`auto` mode** — Claude Code's `auto` runs a background classifier that can deny. pi-go could implement it
   as a straight auto-approve (cheapest, matches today's behaviour exactly) or with classifier-backed safety
   checks. Proposal: start with straight auto-approve; add the classifier only if `dontAsk` proves insufficient.
4. **Rule precedence vs. SOPs** — when an SOP stage declares its own tool allowlist
   (`internal/subagent/agents.go:144`), do permission rules apply on top, or does the SOP list win? Proposal:
   permission rules always apply; SOP allowlists narrow further, never widen.
