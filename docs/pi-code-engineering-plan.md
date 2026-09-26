# Pi Code — Engineering & Product Development Plan

## 1. Product Vision

Pi Code is an AI-native software engineering agent designed to work on real codebases rather than simply answering coding questions.

> Give Pi Code a repository and a goal, and let it understand, investigate, plan, implement, verify, and iterate on the work.

The first version should focus entirely on becoming a high-quality coding agent.

The UI is secondary.

The architecture must make it possible to expose the same agent later through:

- CLI
- Web application
- T3-style interface
- Desktop application
- Mobile application
- IDE integration
- API
- CI/CD integrations

The **Agent Core must remain independent from the UI**.

---

# 2. Product Principles

## 2.1 Agent-first

Do not start by building a chat application.

Start by building the intelligence and execution layer.

The UI should eventually become a client of the Agent Core.

## 2.2 Repository-aware

Pi Code should not treat every request as an isolated prompt.

It needs to understand:

- Repository structure
- Programming languages
- Frameworks
- Dependencies
- Existing architecture
- Coding conventions
- Existing implementations
- Tests
- Configuration
- Git history when useful
- Runtime behavior when available

## 2.3 Plan before execution

For non-trivial tasks:

```text
Understand
    ↓
Investigate
    ↓
Plan
    ↓
Execute
    ↓
Verify
    ↓
Review
    ↓
Iterate
```

The agent should not immediately start modifying files simply because the user asked for a feature.

## 2.4 Verification is part of the agent

Generating code is not success.

Success means:

```text
Code changed
+
Tests passed
+
Build passed
+
Static checks passed
+
Agent reviewed the result
+
Task requirements satisfied
```

## 2.5 Controlled autonomy

Pi Code should support different levels of autonomy:

```text
Ask before every change
        ↓
Ask before risky operations
        ↓
Autonomous implementation
        ↓
Fully autonomous task execution
```

The user should always be able to understand what the agent is doing.

---

# 3. High-Level Architecture

```text
                    ┌─────────────────────┐
                    │      Clients        │
                    │                     │
                    │ CLI / Web / Mobile  │
                    │ IDE / API           │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │    Agent Gateway    │
                    │                     │
                    │ Sessions            │
                    │ Authentication      │
                    │ Streaming           │
                    └──────────┬──────────┘
                               │
                               ▼
              ┌────────────────────────────────┐
              │          Agent Runtime          │
              │                                │
              │ Orchestrator                   │
              │ Planner                        │
              │ Context Manager                │
              │ Tool Manager                   │
              │ Memory                         │
              │ Verification                   │
              │ Review                         │
              └───────────────┬────────────────┘
                              │
             ┌────────────────┼────────────────┐
             ▼                ▼                ▼
      ┌────────────┐   ┌────────────┐   ┌────────────┐
      │ Repository │   │   Tools    │   │   Models   │
      │   Engine   │   │            │   │            │
      │            │   │ Shell      │   │ OpenAI     │
      │ Files      │   │ Git        │   │ Anthropic  │
      │ Search     │   │ Tests      │   │ Gemini     │
      │ Index      │   │ Build      │   │ Local      │
      └────────────┘   └────────────┘   └────────────┘
```

---

# 4. Phase 0 — Technical Foundation

## Goal

Create the project structure and core abstractions without coupling the system to a specific model provider, UI, or execution environment.

## Suggested project structure

```text
pi-code/
│
├── cmd/
│
├── internal/
│   ├── agent/
│   ├── orchestrator/
│   ├── planner/
│   ├── context/
│   ├── memory/
│   ├── tools/
│   ├── repository/
│   ├── execution/
│   ├── verification/
│   ├── review/
│   ├── models/
│   └── session/
│
├── pkg/
│
├── configs/
│
├── tests/
│
├── docs/
│
└── README.md
```

## Core interfaces

Define abstractions for:

- `ModelProvider`
- `Tool`
- `ToolExecutor`
- `Repository`
- `FileSystem`
- `CommandExecutor`
- `GitProvider`
- `ContextProvider`
- `MemoryStore`
- `SessionStore`
- `Verifier`

The goal is to avoid hard-coupling the Agent Runtime directly to:

- One LLM provider
- One filesystem implementation
- One shell
- One Git implementation
- One database
- One UI

---

# 5. Phase 1 — Repository Intelligence

## Goal

Enable Pi Code to understand a repository before attempting to modify it.

The agent should be able to answer:

> What is this repository?

before answering:

> What code should I change?

## 5.1 Repository discovery

Automatically detect:

- Programming languages
- Frameworks
- Package managers
- Build systems
- Test frameworks
- Entry points
- Configuration files
- Docker configuration
- CI/CD configuration
- Database migrations
- API definitions
- Documentation
- Environment configuration

Example:

```text
Go
├── Gin
├── PostgreSQL
├── Redis
├── RabbitMQ
├── Docker
└── GitLab CI
```

## 5.2 Repository map

Create an internal representation of the repository:

```text
Repository
│
├── Applications
│   ├── API
│   └── Worker
│
├── Domain
│   ├── Payment
│   ├── Merchant
│   └── Transaction
│
├── Infrastructure
│   ├── Database
│   ├── Redis
│   └── RabbitMQ
│
└── Tests
```

## 5.3 Code search

Support multiple search strategies.

### Exact search

Example:

```text
Find every reference to CreatePayment.
```

Possible implementation:

- ripgrep
- indexed text search

### Semantic search

Example:

```text
Where do we validate transaction amount?
```

Potential implementation:

- Embeddings
- Vector search
- Semantic retrieval

### Structural search

Example:

```text
Find all HTTP handlers that call the payment service.
```

Potential implementation:

- AST
- Language server capabilities
- Symbol indexes

---

# 6. Phase 2 — Context Engine

## Goal

Decide what information the model actually needs for the current task.

Do not send the entire repository to the model.

```text
User request
      ↓
Task understanding
      ↓
Relevant files
      ↓
Relevant symbols
      ↓
Relevant tests
      ↓
Relevant documentation
      ↓
Relevant history
      ↓
Context package
      ↓
Model
```

## 6.1 Context sources

### Repository

- Files
- Symbols
- Imports
- Call graph
- Tests

### Git

- Recent commits
- Blame
- Changed files
- Previous implementations

### Documentation

- README
- ADRs
- API documentation
- Architecture documentation

### Runtime

Later:

- Logs
- Stack traces
- Metrics
- Test output

## 6.2 Context budget

Track:

```text
Model context budget
Used tokens
Remaining tokens
Priority
Source
Relevance
```

Retrieved context should be ranked instead of blindly concatenated.

---

# 7. Phase 3 — Agent Runtime

## Goal

Build the core agent loop.

```text
Observe
  ↓
Think
  ↓
Choose Tool
  ↓
Execute Tool
  ↓
Observe Result
  ↓
Update State
  ↓
Continue
```

Example task:

```text
User:
"Add idempotency support to the payment endpoint."

Agent:
1. Inspect endpoint
2. Find payment service
3. Find transaction model
4. Find existing Redis usage
5. Inspect tests
6. Create implementation plan
7. Modify code
8. Add tests
9. Run tests
10. Fix failures
11. Review diff
12. Report result
```

---

# 8. Tool System

Tools should be first-class components.

Each tool should expose:

```text
name
description
input schema
permissions
risk level
executor
output
```

## 8.1 Initial tools

### File tools

```text
read_file
write_file
edit_file
delete_file
list_directory
search_files
```

### Shell

```text
run_command
```

### Git

```text
git_status
git_diff
git_log
git_show
git_branch
```

### Code intelligence

```text
find_symbol
find_references
find_definition
search_code
```

### Verification

```text
run_tests
run_build
run_linter
run_formatter
```

---

# 9. Tool Safety

Every tool should have a risk classification.

```text
LOW
- read_file
- search_files
- git_status

MEDIUM
- write_file
- run_tests
- git checkout

HIGH
- rm
- database migration
- git push
- deployment
```

The Agent Runtime should enforce permissions.

Example:

```text
User approval required
        ↓
HIGH risk tool
        ↓
Show command
        ↓
User approves
        ↓
Execute
```

---

# 10. Phase 4 — Planning Engine

## Goal

Convert a natural-language engineering request into an executable plan.

Example:

```text
Task:
"Add Redis-based idempotency."

Plan:

1. Inspect payment endpoint
2. Identify request identifier
3. Inspect existing Redis abstraction
4. Define idempotency key strategy
5. Implement middleware/service layer
6. Handle duplicate requests
7. Add unit tests
8. Add integration tests
9. Run full test suite
10. Review implementation
```

Each plan step should contain:

```text
ID
Description
Dependencies
Expected outcome
Files potentially affected
Verification
Status
```

---

# 11. Execution State Machine

Do not rely only on conversation history.

Maintain explicit task state.

```text
CREATED
   ↓
ANALYZING
   ↓
PLANNED
   ↓
EXECUTING
   ↓
VERIFYING
   ↓
REVIEWING
   ↓
COMPLETED
```

Failure path:

```text
VERIFYING
    ↓
FAILED
    ↓
DIAGNOSING
    ↓
FIXING
    ↓
VERIFYING
```

This state machine should be persisted so tasks can be resumed.

---

# 12. Phase 5 — Code Modification Engine

## Goal

Provide reliable code editing.

Do not rely exclusively on naive string replacement.

## 12.1 Patch-based editing

```text
old code
   ↓
patch
   ↓
new code
```

## 12.2 AST-aware editing

Eventually support operations such as:

```text
Add method
Rename symbol
Add import
Modify function
Add interface implementation
```

## 12.3 Model-generated patch

Allow the model to generate a patch, then validate it before applying it.

---

# 13. Phase 6 — Verification Engine

## Goal

Make verification a first-class part of the agent.

After meaningful implementation:

```text
Format
  ↓
Compile
  ↓
Unit Tests
  ↓
Integration Tests
  ↓
Static Analysis
  ↓
Diff Review
```

The verification engine should discover project-specific commands.

Example:

### Go

```text
go test ./...
go vet ./...
go build ./...
```

### Node

```text
npm test
npm run build
npm run lint
```

### Python

```text
pytest
ruff
mypy
```

Pi Code should discover these commands rather than hard-code one global workflow.

---

# 14. Self-Healing Loop

If verification fails:

```text
Test failure
     ↓
Capture output
     ↓
Understand failure
     ↓
Locate responsible code
     ↓
Create fix
     ↓
Apply fix
     ↓
Run verification again
```

Example:

```text
go test ./...

FAIL:
expected 201
got 500

        ↓

Agent investigates

        ↓

Finds missing dependency mock

        ↓

Fixes implementation/test

        ↓

Runs tests

        ↓

PASS
```

Set an explicit repair limit.

```text
MAX_REPAIR_ITERATIONS = 5
```

The agent must stop instead of looping forever.

---

# 15. Phase 7 — Code Review Agent

After implementation, run a dedicated review stage.

```text
Implementation Agent
        ↓
Changed Files
        ↓
Review Agent
        ↓
Findings
```

The reviewer should inspect:

## Correctness

- Does the implementation satisfy the requirement?
- Are edge cases handled?

## Architecture

- Does it follow existing patterns?
- Is responsibility placed correctly?

## Reliability

- Race conditions
- Timeouts
- Retries
- Idempotency
- Error handling

## Security

- Authentication
- Authorization
- Input validation
- Secrets
- Injection risks

## Maintainability

- Complexity
- Naming
- Duplication
- Test coverage

The reviewer should initially report findings rather than automatically modifying code.

Later, review findings can feed back into the execution loop.

---

# 16. Multi-Agent Architecture

Do not start with dozens of agents.

Start with one orchestrator and specialized roles.

```text
                    Orchestrator
                         │
          ┌──────────────┼──────────────┐
          ▼              ▼              ▼
       Planner        Coder         Reviewer
          │              │              │
          └──────────────┼──────────────┘
                         ▼
                     Verifier
```

Possible later roles:

- Researcher
- Architect
- Coder
- Debugger
- Tester
- Security Reviewer
- Code Reviewer
- Documentation Agent

The orchestrator controls these roles.

---

# 17. Model Abstraction

Pi Code must not be tied to one model provider.

Create:

```text
ModelProvider
```

with capabilities such as:

```text
chat
tool calling
structured output
streaming
vision
reasoning
```

Potential providers:

- OpenAI
- Anthropic
- Google
- Local/Ollama
- Other compatible providers

---

# 18. Model Routing

Later, introduce intelligent model routing.

```text
Task
  ↓
Classifier
  ↓
┌───────────────────────────────┐
│ Coding task  → Model A        │
│ Fast lookup  → Model B        │
│ Large context → Model C       │
│ Local/private → Model D       │
│ Review       → Model E        │
└───────────────────────────────┘
```

The objective is not simply to use the strongest model.

The objective is to use the appropriate model for the task while balancing:

- Quality
- Cost
- Latency
- Reliability

---

# 19. Memory

Separate memory into distinct categories.

## 19.1 Short-term memory

Current task state:

```text
What am I doing?
What did I try?
What failed?
What remains?
```

## 19.2 Repository memory

Persistent repository knowledge:

```text
Architecture
Conventions
Important modules
Known constraints
Build commands
Testing strategy
```

## 19.3 User memory

Optional preferences:

```text
Preferred coding style
Approval preferences
Preferred tools
Communication preferences
```

Do not mix these memory types.

---

# 20. Session System

Every agent run should have a persistent session.

```text
Session
├── User request
├── Repository
├── Agent state
├── Plan
├── Tool calls
├── Files changed
├── Verification
├── Review
└── Final result
```

This enables:

- Resume
- Retry
- Debugging
- Auditability
- Observability

---

# 21. Observability

Pi Code itself needs strong observability.

Track:

```text
Session duration
Model latency
Token usage
Tool calls
Tool failures
Files read
Files changed
Tests executed
Verification failures
Repair iterations
Final status
```

Later track:

```text
Cost per task
Success rate
Average repair attempts
Model success rate
Tool failure rate
```

---

# 22. CLI — First Real Interface

Before Web or Mobile, build a CLI.

Examples:

```bash
pi init
```

```bash
pi
```

```bash
pi "Add retry logic to the payment service"
```

Possible commands:

```bash
pi init
pi run
pi plan
pi review
pi test
pi status
pi history
pi config
```

Interactive mode:

```text
$ pi

Pi Code

Repository: payment-service
Branch: feature/idempotency

> Add idempotency support to payment creation.

Analyzing repository...

✓ Repository analyzed
✓ Relevant files identified

Creating plan...

1. Inspect payment flow
2. Add idempotency mechanism
3. Add tests
4. Run verification

Proceed? [Y/n]
```

The CLI is intentionally first because it forces the Agent Core to work without hiding problems behind a UI.

---

# 23. API Layer

Once the Agent Core works through the CLI, expose it through an API.

```text
Client
  ↓
API
  ↓
Session Service
  ↓
Agent Runtime
  ↓
Tools
```

Support:

```text
Create session
Send message
Stream agent events
Approve tool
Cancel task
Get task status
Get diff
Get logs
```

Streaming should be event-based.

Example events:

```text
agent.started
agent.thinking
tool.started
tool.completed
file.changed
verification.started
verification.failed
agent.retrying
agent.completed
```

---

# 24. Event Architecture

Define an internal event model early.

```text
AgentStarted
PlanCreated
ToolCallStarted
ToolCallCompleted
FileRead
FileChanged
CommandStarted
CommandCompleted
VerificationStarted
VerificationFailed
ReviewStarted
ReviewFinding
TaskCompleted
TaskFailed
```

The future UI should consume these events instead of implementing its own agent logic.

---

# 25. Phase 8 — T3 / Web Experience

Only start this phase after the Agent Core is reliable.

The first Web UI should expose:

```text
┌────────────────────────────────────────────┐
│ Pi Code                                    │
├──────────────────┬─────────────────────────┤
│                  │                         │
│ Conversation     │ Agent Activity          │
│                  │                         │
│ User request     │ Analyzing repository    │
│                  │                         │
│                  │ Reading payment.go      │
│                  │                         │
│                  │ Running tests            │
│                  │                         │
│                  │ ✓ Tests passed           │
│                  │                         │
├──────────────────┴─────────────────────────┤
│ Ask Pi Code...                             │
└────────────────────────────────────────────┘
```

Expose engineering state:

```text
PLAN
✓ Analyze repository
✓ Inspect payment flow
→ Implement idempotency
○ Add tests
○ Verify
```

Also expose changed files:

```text
Changed files: 4

+ payment_service.go
+ idempotency.go
~ payment_handler.go
+ idempotency_test.go
```

---

# 26. Human-in-the-Loop UX

Make approval decisions easy.

Example:

```text
Pi wants to run:

go test ./...

[Allow] [Always Allow] [Reject]
```

For dangerous operations:

```text
Pi wants to execute:

git push origin main

This operation can modify the remote repository.

[Approve] [Reject]
```

---

# 27. Phase 9 — Advanced Repository Intelligence

After the basic agent works, improve its code understanding.

## AST indexing

Build symbol-level indexes.

## Dependency graph

```text
Handler
  ↓
Service
  ↓
Repository
  ↓
Database
```

## Call graph

```text
CreatePayment
    ↓
ValidatePayment
    ↓
CreateTransaction
    ↓
PublishEvent
```

## Change impact analysis

Before changing a function:

```text
Function changed
      ↓
Find callers
      ↓
Find tests
      ↓
Find dependent modules
      ↓
Determine verification scope
```

This improves agent reliability significantly.

---

# 28. Phase 10 — Git Intelligence

Pi Code should eventually understand Git deeply.

Features:

```text
git diff
git history
commit analysis
blame
branch comparison
PR preparation
```

Example:

> Why is this code implemented this way?

Pi Code can inspect:

```text
Current implementation
       +
Git history
       +
Relevant commits
       +
Tests
```

and provide an evidence-based explanation.

---

# 29. Phase 11 — Pull Request Workflow

Eventually support:

```text
User request
     ↓
Pi implements
     ↓
Tests
     ↓
Review
     ↓
Git diff
     ↓
Commit
     ↓
Pull Request
```

Generate:

```text
PR title
PR description
Summary
Changed files
Testing performed
Potential risks
```

---

# 30. Phase 12 — Autonomous Engineering Tasks

Long-term, Pi Code should handle ambiguous engineering tasks.

Example:

> Investigate why payment transactions occasionally remain pending.

Potential workflow:

```text
Investigate
    ↓
Search code
    ↓
Search logs
    ↓
Inspect transaction state machine
    ↓
Identify possible failure paths
    ↓
Create hypothesis
    ↓
Verify hypothesis
    ↓
Implement fix
    ↓
Add regression test
    ↓
Run verification
    ↓
Prepare PR
```

This is the long-term direction of the product.

---

# 31. Security Model

Security must be designed into the architecture.

## Sandbox

Commands should run in an isolated environment where possible.

## Permission model

```text
read
write
execute
network
git
deployment
```

## Secret protection

Never expose sensitive values to the model unless explicitly required and safely scoped:

```text
API keys
passwords
tokens
private keys
```

## Command policy

Block dangerous patterns by default.

---

# 32. Testing Strategy

Pi Code itself requires multiple levels of testing.

## Unit tests

For:

- Planner
- Context engine
- Tool manager
- State machine
- Model adapters

## Integration tests

Test:

```text
Agent
+
Repository
+
Tools
+
Model
```

## Agent evaluation

Create a benchmark repository suite containing tasks such as:

```text
Bug fixing
Feature implementation
Refactoring
Testing
Documentation
Security fixes
Performance improvements
```

Measure:

```text
Task success
Test success
Regression rate
Token usage
Time
Number of tool calls
Number of retries
```

---

# 33. Evaluation Framework

Do not evaluate the agent based only on whether the generated code looks good.

Use objective evaluation:

```text
Task
 ↓
Agent
 ↓
Patch
 ↓
Automated tests
 ↓
Static analysis
 ↓
Human review
```

Create benchmark levels.

### L1 — Simple changes

Single-file changes and simple fixes.

### L2 — Multi-file changes

Changes involving several modules.

### L3 — Repository-level features

Features requiring architecture understanding.

### L4 — Debugging

Diagnose and fix existing failures.

### L5 — Ambiguous engineering tasks

Tasks with incomplete implementation guidance.

### L6 — Autonomous investigation

Investigate, hypothesize, verify, implement, and test.

---

# 34. Cost Management

Track:

```text
Input tokens
Output tokens
Reasoning tokens where available
Tool calls
Total cost
```

Introduce later:

```text
Context caching
Result caching
Repository indexing
Model routing
Prompt compression
```

Do not repeatedly send the same repository context.

---

# 35. Configuration

Pi Code should eventually support a project configuration file.

Example:

```yaml
project:
  language: auto

agent:
  autonomy: supervised

models:
  primary: ...
  fast: ...
  review: ...

verification:
  tests: auto
  lint: auto
  build: auto

permissions:
  shell: ask
  write: ask
  git_push: deny
```

The exact configuration format can evolve.

---

# 36. MVP Definition

Do not implement everything before getting a working agent.

The first real milestone should be:

```text
Repository
    ↓
User request
    ↓
Repository analysis
    ↓
Plan
    ↓
Tool execution
    ↓
Code modification
    ↓
Run tests
    ↓
Fix failures
    ↓
Review diff
    ↓
Final response
```

If Pi Code can reliably do this from a terminal, the core product is already real.

---

# 37. MVP Definition of Done

## Repository

- [ ] Open a local repository
- [ ] Detect project type
- [ ] Explore files
- [ ] Search code
- [ ] Understand relevant files

## Agent

- [ ] Receive a task
- [ ] Build a plan
- [ ] Execute multiple steps
- [ ] Maintain task state
- [ ] Call tools
- [ ] Recover from tool failures

## Code

- [ ] Read files
- [ ] Modify files
- [ ] Create files
- [ ] Produce a clean diff

## Verification

- [ ] Detect test commands
- [ ] Run tests
- [ ] Parse failures
- [ ] Attempt fixes
- [ ] Re-run verification

## Review

- [ ] Review changed files
- [ ] Detect obvious problems
- [ ] Report remaining concerns

## UX

- [ ] CLI
- [ ] Streaming output
- [ ] Tool activity
- [ ] Approval prompts
- [ ] Final result

---

# 38. Post-MVP Roadmap

```text
MVP
 │
 ├── Better Context Engine
 │
 ├── AST / Code Intelligence
 │
 ├── Better Planning
 │
 ├── Multi-Agent Review
 │
 ├── Model Routing
 │
 ├── Git Intelligence
 │
 ├── PR Automation
 │
 ├── Remote Execution
 │
 ├── API
 │
 └── Web UI
```

Only after the Agent Core is stable should the product experience become the primary focus.

---

# 39. Recommended Development Order

Implement in this order:

```text
01. Project foundation
02. Core interfaces
03. Repository abstraction
04. File/search tools
05. Shell tool
06. Git tools
07. Model abstraction
08. Basic agent loop
09. Repository discovery
10. Context engine
11. Planning engine
12. Tool orchestration
13. File editing
14. Verification engine
15. Self-healing loop
16. Task state machine
17. Code review
18. CLI
19. Session persistence
20. Observability
21. Evaluation framework
22. Model routing
23. Advanced code intelligence
24. Git/PR workflow
25. API
26. Web/T3 client
27. Mobile client
```

---

# 40. Critical Architectural Rule

The most important architectural decision is the separation between clients and the Agent Core.

```text
                 CLIENTS
                    │
                    ▼
              ┌───────────┐
              │  API/CLI  │
              └─────┬─────┘
                    │
                    ▼
             ┌──────────────┐
             │ Agent Runtime │
             └──────┬───────┘
                    │
        ┌───────────┼───────────┐
        ▼           ▼           ▼
     Context      Tools       Models
        │           │           │
        └───────────┼───────────┘
                    ▼
               Repository
```

**Never let the Web/Mobile UI become the Agent.**

The Agent should be a standalone engineering system.

This allows Pi Code to eventually support:

```text
CLI
Web
Mobile
IDE
CI/CD
GitHub/GitLab
API
```

without rebuilding the intelligence layer.

---

# 41. First Milestone

The first milestone is:

> Pi Code can take a real Git repository and a natural-language engineering task, investigate the repository, create a plan, modify the code, run verification, fix failures, and return a final diff/result without requiring the user to manually orchestrate the process.

Everything else should build on top of this.

## Immediate next step

Break:

```text
Phase 0
→ Phase 1
→ Phase 2
→ Basic Agent Loop
```

into implementation tasks and start building the Agent Core.

Do not start T3 yet.
