package tui

import (
	"context"
	"sync"

	"github.com/dimetron/pi-go/internal/permission"
)

// PermissionBridge carries the permission engine from the point where the
// tools are built to the point where the UI exists to answer questions.
//
// It exists because of an ordering constraint that is not obvious from either
// end. The core tools are constructed in the deferred-init goroutine, because
// they are needed by the agent and building them is slow. The model that can
// show an approval prompt is constructed later still, by tui.Run. So the engine
// must be handed to the tool layer before there is anything that could approve,
// and the approver attached afterwards.
//
// The pre-attach behaviour is the important part: until a UI is attached the
// bridge refuses, it does not allow. A question that arrives in that window has
// nowhere to be shown, and approving it silently would mean a destructive call
// running with nobody asked — the one failure this whole feature exists to
// prevent.

// PermissionBridge is both the permission.Approver handed to the tool layer and
// the handle the TUI uses to attach its own prompt.
type PermissionBridge struct {
	mu       sync.RWMutex
	engine   *permission.Engine
	approver permission.Approver
}

// NewPermissionBridge wraps an engine. A nil engine means no policy was
// configured, in which case there is nothing to ask about and Approve defers to
// the attached UI exactly as it would for any other request.
func NewPermissionBridge(engine *permission.Engine) *PermissionBridge {
	return &PermissionBridge{engine: engine}
}

// Approve satisfies permission.Approver, so it can be passed straight to
// permission.WithApprover.
func (b *PermissionBridge) Approve(ctx context.Context, req permission.Request) (bool, error) {
	if b == nil {
		return false, nil
	}
	b.mu.RLock()
	approver := b.approver
	b.mu.RUnlock()
	if approver == nil {
		return false, nil
	}
	return approver(ctx, req)
}

// PermissionApprover is what Config.PermissionBridge accepts. Both a settled
// bridge and a pending one satisfy it, so the config field does not have to
// know which stage of startup it is holding.
//
// attach is unexported on purpose: the TUI owns the prompt, so nothing outside
// this package can install an approver and start answering permission
// questions on the user's behalf.
type PermissionApprover interface {
	Approve(ctx context.Context, req permission.Request) (bool, error)
	attach(permission.Approver)
	// Engine exposes the policy so the UI can show and change the mode. A
	// pending bridge has none yet — that is the whole of what is pending — so
	// it returns nil and the mode keys stay inert until the engine arrives.
	Engine() *permission.Engine
}

// attach installs the UI approver. It is called once, by tui.Run, after the
// model exists.
func (b *PermissionBridge) attach(a permission.Approver) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.approver = a
	b.mu.Unlock()
}

// Engine exposes the engine so the TUI can show the current mode and cycle it.
func (b *PermissionBridge) Engine() *permission.Engine {
	if b == nil {
		return nil
	}
	return b.engine
}

// attachPermissionPrompt wires this model's approval prompt to the bridge, if
// the surface provided one. It is a no-op otherwise, so a TUI used in tests or
// without a permission engine keeps working.
func (m *model) attachPermissionPrompt() {
	if m.cfg.PermissionBridge == nil {
		return
	}
	m.cfg.PermissionBridge.attach(m.Approver())
}

// PendingBridge is a bridge whose engine has not been built yet. It is what
// lets the TUI start immediately while the permission engine is still being
// constructed alongside the tools.
//
// It exists because the alternative — blocking the UI until the engine exists —
// puts the whole startup screen behind a slow sandbox or an MCP handshake, and
// the startup screen is the only thing making that wait tolerable. So the UI
// comes up first and adopts the engine when it arrives.
type PendingBridge struct {
	ready <-chan *PermissionBridge
	// once guards the single receive from ready; arrived is closed once the
	// bridge is in hand, so late callers do not re-read the channel.
	once    sync.Once
	arrived chan struct{}
	bridge  *PermissionBridge
	// mu guards approver, which the Update loop installs before the engine
	// exists and attach must install on the bridge that eventually shows up.
	mu       sync.Mutex
	approver permission.Approver
}

// NewPendingPermissionBridge wraps the channel the init goroutine publishes
// the real bridge on. The channel is expected to be buffered: a send that
// blocks because nobody has taken the value yet would stall deferred init for
// no reason.
func NewPendingPermissionBridge(ready <-chan *PermissionBridge) *PendingBridge {
	return &PendingBridge{
		ready:   ready,
		arrived: make(chan struct{}),
	}
}

// Approve blocks until the engine exists, then delegates. Blocking is the
// right call here and only here: the alternative is deciding a permission
// question before the policy that would answer it has been read, which means
// answering every first question with a guess.
func (b *PendingBridge) Approve(ctx context.Context, req permission.Request) (bool, error) {
	bridge, err := b.await(ctx)
	if err != nil || bridge == nil {
		return false, err
	}
	return bridge.Approve(ctx, req)
}

// Engine returns nil while the engine is still being built.
//
// It deliberately does not block. The mode keys are a UI affordance, and a UI
// that froze on the first Shift+Tab until a slow sandbox finished loading
// would be worse than one where the key does nothing yet. The engine is
// reached through the ordinary path anyway: the first permission question waits
// for it, so nothing is decided against a policy that has not been read.
func (b *PendingBridge) Engine() *permission.Engine {
	if b == nil {
		return nil
	}
	select {
	case <-b.arrived:
		if b.bridge == nil {
			return nil
		}
		return b.bridge.Engine()
	default:
		return nil
	}
}

// attach forwards the UI approver to the real bridge, whenever it turns up.
func (b *PendingBridge) attach(a permission.Approver) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.approver = a
	b.mu.Unlock()

	// One goroutine per attach, not per caller: attach is called once by
	// tui.Run, and this way a second attach still reaches the bridge.
	go func() {
		bridge, err := b.await(context.Background())
		if err != nil || bridge == nil {
			return
		}
		bridge.attach(a)
	}()
}

// await returns the real bridge, receiving from the channel exactly once no
// matter how many callers race for it.
//
// The blocking receive is deliberately *outside* the once: doing it inside
// would make the second caller wait for a second value on a channel that only
// ever gets one, while holding the mutex the first caller needs to publish —
// a deadlock. So one goroutine takes the value, stores it, and closes arrived;
// everyone else just waits on the close.
func (b *PendingBridge) await(ctx context.Context) (*PermissionBridge, error) {
	b.once.Do(func() {
		go func() {
			bridge, ok := <-b.ready
			if ok {
				b.bridge = bridge
			}
			close(b.arrived)
		}()
	})
	select {
	case <-b.arrived:
		return b.bridge, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
