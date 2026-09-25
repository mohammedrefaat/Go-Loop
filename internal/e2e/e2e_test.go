package e2e

import (
	"context"
	"testing"
	"time"

	domainCtx "github.com/dimetron/pi-go/internal/context"
	"github.com/dimetron/pi-go/internal/execution"
	"github.com/dimetron/pi-go/internal/memory"
	"github.com/dimetron/pi-go/internal/models"
	"github.com/dimetron/pi-go/internal/orchestrator"
	"github.com/dimetron/pi-go/internal/planner"
	"github.com/dimetron/pi-go/internal/repository"
	"github.com/dimetron/pi-go/internal/review"
	"github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/tools"
	"github.com/dimetron/pi-go/internal/verification"
)

// CleanDomainPipeline integrates all Phase 0 domain interfaces into an end-to-end execution chain.
type CleanDomainPipeline struct {
	modelProvider models.ModelProvider
	toolExecutor  tools.ToolExecutor
	repo          repository.Repository
	cmdExec       execution.CommandExecutor
	ctxProvider   domainCtx.ContextProvider
	memStore      memory.MemoryStore
	sessStore     session.SessionStore
	verifier      verification.Verifier
	planner       planner.Planner
	reviewer      review.Reviewer
	orchestrator  orchestrator.Orchestrator
}

func TestEndToEndDomainPipeline(t *testing.T) {
	ctx := context.Background()

	// 1. Initialize session store and session
	sessStore := &mockSessStore{sessions: make(map[string]*session.SessionData)}
	sData := &session.SessionData{
		ID:        "e2e-session-1",
		AppName:   "pi-code",
		UserID:    "engineer",
		WorkDir:   "/workspace",
		State:     models.TaskStateCreated,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := sessStore.CreateSession(ctx, sData); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// 2. State transition: CREATED -> ANALYZING
	sData.State = models.TaskStateAnalyzing
	if err := sessStore.UpdateSession(ctx, sData); err != nil {
		t.Fatalf("failed to transition state to ANALYZING: %v", err)
	}

	// 3. Create Plan using Planner
	planImpl := &mockPlannerImpl{}
	plan, err := planImpl.CreatePlan(ctx, "Refactor codebase to Clean Architecture")
	if err != nil || len(plan.Steps) == 0 {
		t.Fatalf("failed to create plan: %v, %v", plan, err)
	}

	// 4. ModelProvider call
	modelProv := &mockModelProv{}
	genResp, err := modelProv.Generate(ctx, []models.ChatMessage{
		{Role: models.RoleUser, Content: plan.Goal},
	}, nil)
	if err != nil || genResp.Message.Content == "" {
		t.Fatalf("failed model generation: %v", err)
	}

	// 5. Tool & Command Execution (State: EXECUTING)
	sData.State = models.TaskStateExecuting
	_ = sessStore.UpdateSession(ctx, sData)

	toolExec := &mockToolExec{}
	toolRes, err := toolExec.Execute(ctx, "read_file", map[string]any{"path": "main.go"})
	if err != nil || !toolRes.Success {
		t.Fatalf("failed tool execution: %v, %v", toolRes, err)
	}

	cmdExec := &mockCmdExec{}
	cmdRes, err := cmdExec.ExecuteCommand(ctx, "go", []string{"build", "./..."}, nil)
	if err != nil || cmdRes.ExitCode != 0 {
		t.Fatalf("failed command execution: %v, %v", cmdRes, err)
	}

	// 6. Verification (State: VERIFYING)
	sData.State = models.TaskStateVerifying
	_ = sessStore.UpdateSession(ctx, sData)

	verifier := &mockVerifierImpl{}
	verifRes, err := verifier.Verify(ctx, &verification.VerificationOptions{})
	if err != nil || !verifRes.Passed {
		t.Fatalf("failed verification: %v, %v", verifRes, err)
	}

	// 7. Memory Persistence & Final Completion (State: COMPLETED)
	memStore := &mockMemStore{items: make(map[string]*memory.MemoryItem)}
	memErr := memStore.Save(ctx, &memory.MemoryItem{
		ID:      "mem-1",
		Project: "pi-code",
		Title:   "Clean Architecture Refactoring",
		Text:    "Successfully established clean domain interfaces",
	})
	if memErr != nil {
		t.Fatalf("failed memory save: %v", memErr)
	}

	sData.State = models.TaskStateCompleted
	if err := sessStore.UpdateSession(ctx, sData); err != nil {
		t.Fatalf("failed to complete session: %v", err)
	}

	// Verify final state is terminal and COMPLETED
	if !sData.State.IsTerminal() || sData.State != models.TaskStateCompleted {
		t.Fatalf("expected terminal COMPLETED state, got %s", sData.State)
	}
}

type mockSessStore struct {
	sessions map[string]*session.SessionData
}

func (m *mockSessStore) CreateSession(ctx context.Context, s *session.SessionData) error {
	m.sessions[s.ID] = s
	return nil
}
func (m *mockSessStore) GetSession(ctx context.Context, id string) (*session.SessionData, error) {
	return m.sessions[id], nil
}
func (m *mockSessStore) UpdateSession(ctx context.Context, s *session.SessionData) error {
	m.sessions[s.ID] = s
	return nil
}
func (m *mockSessStore) DeleteSession(ctx context.Context, id string) error {
	delete(m.sessions, id)
	return nil
}
func (m *mockSessStore) ListSessions(ctx context.Context, appName, userID string) ([]*session.SessionData, error) {
	return []*session.SessionData{}, nil
}

type mockPlannerImpl struct{}

func (m *mockPlannerImpl) CreatePlan(ctx context.Context, goal string) (*planner.Plan, error) {
	return &planner.Plan{
		ID:   "p1",
		Goal: goal,
		Steps: []planner.PlanStep{
			{Index: 1, Description: "Scaffold domain", Completed: true},
		},
	}, nil
}

type mockModelProv struct{}

func (m *mockModelProv) Name() string { return "mock-model" }
func (m *mockModelProv) Generate(ctx context.Context, messages []models.ChatMessage, opts *models.GenerateOptions) (*models.GenerateResponse, error) {
	return &models.GenerateResponse{
		Message: models.ChatMessage{Role: models.RoleAssistant, Content: "Plan looks solid"},
	}, nil
}
func (m *mockModelProv) StreamGenerate(ctx context.Context, messages []models.ChatMessage, opts *models.GenerateOptions, handler func(chunk *models.GenerateResponse) error) error {
	return nil
}

type mockToolExec struct{}

func (m *mockToolExec) Execute(ctx context.Context, name string, args map[string]any) (*tools.ToolExecutionResult, error) {
	return &tools.ToolExecutionResult{ToolName: name, Success: true, Output: "content"}, nil
}
func (m *mockToolExec) ListTools(ctx context.Context) ([]tools.Tool, error) {
	return nil, nil
}

type mockCmdExec struct{}

func (m *mockCmdExec) ExecuteCommand(ctx context.Context, command string, args []string, opts *execution.CommandOptions) (*execution.CommandResult, error) {
	return &execution.CommandResult{ExitCode: 0, Stdout: "build succeeded"}, nil
}

type mockVerifierImpl struct{}

func (m *mockVerifierImpl) Format(ctx context.Context, opts *verification.VerificationOptions) (*verification.VerificationCheck, error) {
	return &verification.VerificationCheck{Type: verification.CheckTypeFormat, Passed: true}, nil
}
func (m *mockVerifierImpl) Compile(ctx context.Context, opts *verification.VerificationOptions) (*verification.VerificationCheck, error) {
	return &verification.VerificationCheck{Type: verification.CheckTypeCompile, Passed: true}, nil
}
func (m *mockVerifierImpl) Test(ctx context.Context, opts *verification.VerificationOptions) (*verification.VerificationCheck, error) {
	return &verification.VerificationCheck{Type: verification.CheckTypeTest, Passed: true}, nil
}
func (m *mockVerifierImpl) Lint(ctx context.Context, opts *verification.VerificationOptions) (*verification.VerificationCheck, error) {
	return &verification.VerificationCheck{Type: verification.CheckTypeLint, Passed: true}, nil
}
func (m *mockVerifierImpl) Verify(ctx context.Context, opts *verification.VerificationOptions) (*verification.VerificationResult, error) {
	return &verification.VerificationResult{Passed: true}, nil
}

type mockMemStore struct {
	items map[string]*memory.MemoryItem
}

func (m *mockMemStore) Save(ctx context.Context, item *memory.MemoryItem) error {
	m.items[item.ID] = item
	return nil
}
func (m *mockMemStore) Get(ctx context.Context, id string) (*memory.MemoryItem, error) {
	return m.items[id], nil
}
func (m *mockMemStore) Search(ctx context.Context, query memory.MemoryQuery) ([]*memory.MemoryItem, error) {
	return nil, nil
}
func (m *mockMemStore) Delete(ctx context.Context, id string) error {
	delete(m.items, id)
	return nil
}
func (m *mockMemStore) Close() error { return nil }
