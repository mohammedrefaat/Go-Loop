package tui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/permission"
)

// neverApprove is an approver that always refuses, used where the answer does
// not matter but the fact that it was consulted does.
func neverApprove(context.Context, permission.Request) (bool, error) { return false, nil }

func alwaysApprove(context.Context, permission.Request) (bool, error) { return true, nil }

func TestPermissionBridge_RefusesBeforeAUIAttaches(t *testing.T) {
	b := NewPermissionBridge(testEngine())
	ok, err := b.Approve(context.Background(), permReq("bash", "rm -rf /"))
	if err != nil {
		t.Fatalf("Approve returned an error: %v", err)
	}
	// A question nobody was shown must not be approved. This is the window
	// between the tools being built and the model existing.
	if ok {
		t.Fatal("a request was approved before any UI was attached")
	}
}

func TestPermissionBridge_DelegatesOnceAttached(t *testing.T) {
	b := NewPermissionBridge(testEngine())
	b.attach(alwaysApprove)
	ok, err := b.Approve(context.Background(), permReq("bash", "ls"))
	if err != nil || !ok {
		t.Fatalf("Approve = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestPermissionBridge_AttachPropagatesAnError(t *testing.T) {
	b := NewPermissionBridge(testEngine())
	want := context.Canceled
	b.attach(func(context.Context, permission.Request) (bool, error) { return false, want })
	if _, err := b.Approve(context.Background(), permReq("bash", "ls")); err != want {
		t.Fatalf("Approve error = %v, want %v", err, want)
	}
}

func TestPermissionBridge_NilEngineStillConsultsTheUI(t *testing.T) {
	// A nil engine means no policy was configured, not that requests are
	// pre-approved. The UI still gets asked, because "no policy" and "no
	// question" are different things and only the bridge can tell them apart.
	b := NewPermissionBridge(nil)
	if ok, _ := b.Approve(context.Background(), permReq("bash", "ls")); ok {
		t.Fatal("a request was approved with no UI attached")
	}
	b.attach(alwaysApprove)
	if ok, err := b.Approve(context.Background(), permReq("bash", "ls")); err != nil || !ok {
		t.Fatalf("Approve = (%v, %v), want (true, nil)", ok, err)
	}
	if b.Engine() != nil {
		t.Error("Engine() on a nil engine should be nil")
	}
}

func TestPermissionBridge_NilReceiverIsSafe(t *testing.T) {
	var b *PermissionBridge
	if b.Engine() != nil {
		t.Error("Engine() on a nil bridge should be nil")
	}
	b.attach(alwaysApprove) // must not panic
}

func TestPendingBridge_ApproveWaitsForTheEngineThenRefuses(t *testing.T) {
	ready := make(chan *PermissionBridge, 1)
	p := NewPendingPermissionBridge(ready)

	// Nothing has been published yet, so the answer must not be immediate.
	done := make(chan bool, 1)
	go func() {
		ok, _ := p.Approve(context.Background(), permReq("bash", "ls"))
		done <- ok
	}()
	select {
	case got := <-done:
		t.Fatalf("Approve returned %v before the engine existed", got)
	case <-time.After(50 * time.Millisecond):
	}

	ready <- NewPermissionBridge(testEngine())
	if got := <-done; got {
		t.Fatal("the request was approved with no UI attached")
	}
}

func TestPendingBridge_AttachReachesTheRealBridge(t *testing.T) {
	ready := make(chan *PermissionBridge, 1)
	p := NewPendingPermissionBridge(ready)

	// attach happens first, from tui.Run; the engine arrives afterwards. The
	// approver installed in that order still has to reach the real bridge.
	p.attach(alwaysApprove)

	real := NewPermissionBridge(testEngine())
	ready <- real

	ok, err := answered2(t, func() (bool, error) {
		return p.Approve(context.Background(), permReq("bash", "ls"))
	})
	if err != nil || !ok {
		t.Fatalf("Approve = (%v, %v), want (true, nil) — the approver never arrived", ok, err)
	}
}

func TestPendingBridge_EngineArrivesAfterTheFirstApproveStillReachesTheUI(t *testing.T) {
	ready := make(chan *PermissionBridge, 1)
	p := NewPendingPermissionBridge(ready)

	results := make(chan bool, 1)
	go func() {
		ok, _ := p.Approve(context.Background(), permReq("bash", "ls"))
		results <- ok
	}()
	p.attach(alwaysApprove)
	ready <- NewPermissionBridge(testEngine())

	select {
	case got := <-results:
		if !got {
			t.Fatal("an approver installed while the engine was pending was not consulted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Approve never returned")
	}
}

func TestPendingBridge_EngineIsReadOnceUnderConcurrentApprovers(t *testing.T) {
	ready := make(chan *PermissionBridge, 1)
	p := NewPendingPermissionBridge(ready)
	real := NewPermissionBridge(testEngine())
	// Install the approver on the real bridge before publishing it. attach()
	// forwards asynchronously, so going through it here would have the racers
	// start before the approver landed — a race in the test, not in the code.
	real.attach(alwaysApprove)
	ready <- real
	p.attach(alwaysApprove)

	// Ten goroutines racing for a single-receive channel: every one must get
	// an answer, and none may steal the value the others are waiting on.
	const n = 10
	done := make(chan bool, n)
	for range n {
		go func() {
			ok, _ := p.Approve(context.Background(), permReq("bash", "ls"))
			done <- ok
		}()
	}
	for range n {
		select {
		case ok := <-done:
			if !ok {
				t.Fatal("a concurrent approver was refused")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("a concurrent Approve never returned")
		}
	}
}

func TestPendingBridge_SecondCallerDoesNotDeadlock(t *testing.T) {
	ready := make(chan *PermissionBridge, 1)
	p := NewPendingPermissionBridge(ready)
	p.attach(alwaysApprove)

	// The first caller takes the value off the channel. Every later caller
	// must be able to observe that the engine has already arrived — reading it
	// again would block forever on a channel that will never be written again.
	first := make(chan bool, 1)
	go func() {
		ok, _ := p.Approve(context.Background(), permReq("bash", "ls"))
		first <- ok
	}()
	ready <- NewPermissionBridge(testEngine())
	<-first

	// A second question must reach the same UI rather than hang. attach
	// forwards asynchronously, so wait for the approver to land before asking —
	// otherwise this races the forward and proves nothing about the deadlock.
	deadline := time.Now().Add(2 * time.Second)
	for {
		real, err := p.await(context.Background())
		if err == nil && real != nil {
			real.attach(alwaysApprove)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the engine never arrived")
		}
		time.Sleep(5 * time.Millisecond)
	}

	ok := answered(t, func() bool {
		got, _ := p.Approve(context.Background(), permReq("bash", "ls"))
		return got
	})
	if !ok {
		t.Fatal("the second question was not answered by the attached UI")
	}
}

func TestPendingBridge_ApproveRespectsContextCancellation(t *testing.T) {
	ready := make(chan *PermissionBridge, 1) // never written
	p := NewPendingPermissionBridge(ready)
	p.attach(alwaysApprove)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Approve(ctx, permReq("bash", "ls")); err == nil {
		t.Fatal("Approve ignored a cancelled context instead of returning")
	}
}

func TestPendingBridge_ClosedChannelIsNotAnApproval(t *testing.T) {
	ready := make(chan *PermissionBridge, 1)
	close(ready)
	p := NewPendingPermissionBridge(ready)

	// A closed channel means init failed and no engine will ever exist. The
	// question is unanswerable, so it must be refused, not blocked on forever.
	done := make(chan bool, 1)
	go func() {
		ok, _ := p.Approve(context.Background(), permReq("bash", "ls"))
		done <- ok
	}()
	select {
	case got := <-done:
		if got {
			t.Fatal("a request was approved with no engine and no UI")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Approve blocked forever on a channel that will never be written")
	}
}

func TestPendingBridge_NilReceiverAttachIsSafe(t *testing.T) {
	var p *PendingBridge
	p.attach(alwaysApprove) // must not panic
}

func TestAttachPermissionPrompt_ConnectsTheModelToTheBridge(t *testing.T) {
	m := newTestModel(t)
	b := NewPermissionBridge(testEngine())
	m.cfg.PermissionBridge = b

	// attachPermissionPrompt is what tui.Run calls. The question it raises
	// blocks until the Update loop answers it, so drive that answer from a
	// second goroutine the way a keystroke would.
	m.attachPermissionPrompt()
	go func() {
		for range 40 {
			time.Sleep(5 * time.Millisecond)
			m.permissionPromptMu.Lock()
			p := m.permissionPrompt
			m.permissionPromptMu.Unlock()
			if p != nil {
				m.handlePermissionPromptKey(tea.Key{Code: 'y'})
				return
			}
		}
	}()

	ok, err := answered2(t, func() (bool, error) {
		return b.Approve(context.Background(), permReq("bash", "ls"))
	})
	if err != nil {
		t.Fatalf("Approve returned an error: %v", err)
	}
	if !ok {
		t.Fatal("the model refused a request it should have shown and approved")
	}
}

func TestAttachPermissionPrompt_NoBridgeIsANoOp(t *testing.T) {
	// A TUI constructed without a permission engine must still start.
	newTestModel(t).attachPermissionPrompt()
}

func answered2(t *testing.T, fn func() (bool, error)) (bool, error) {
	t.Helper()
	type result struct {
		ok  bool
		err error
	}
	ch := make(chan result, 1)
	go func() {
		ok, err := fn()
		ch <- result{ok, err}
	}()
	select {
	case r := <-ch:
		return r.ok, r.err
	case <-time.After(2 * time.Second):
		t.Fatal("permission question was never answered")
		return false, nil
	}
}

// testEngine is a default-mode engine with no rules. The bridge tests are
// about the plumbing between the tool layer and the UI, not about rule
// evaluation, which internal/permission covers directly.
func testEngine() *permission.Engine {
	engine, _ := permission.Load("", nil)
	return engine
}
