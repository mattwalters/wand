package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mattwalters/wand/internal/cockpit"
	"github.com/mattwalters/wand/internal/screen"
	"github.com/mattwalters/wand/internal/tuitest"
)

// fakeEngager records what engage mode asked for, the same way fakeBackend
// records judgments: a test that could not see exactly which call was made
// would not be testing much.
type fakeEngager struct {
	acquireErr error
	releaseErr error
	tickResult EngageResult
	tickErr    error

	acquires, releases, ticks int
}

func (f *fakeEngager) AcquireLock() error {
	f.acquires++
	return f.acquireErr
}

func (f *fakeEngager) ReleaseLock() error {
	f.releases++
	return f.releaseErr
}

func (f *fakeEngager) Tick(context.Context) (EngageResult, error) {
	f.ticks++
	return f.tickResult, f.tickErr
}

// boardEngaged builds a model over the sample board with both a writer and
// an engager behind it.
func boardEngaged(t *testing.T, back Backend, eng Engager) Model {
	t.Helper()
	return New(Config{
		Snapshot: cockpit.Sample(),
		Backend:  back,
		Engager:  eng,
		Width:    screen.DefaultWidth,
		Height:   screen.DefaultHeight,
	})
}

// Pressing 'e' with no Engager wired must refuse and say why, the same
// shape a nil Backend already uses for a write.
func TestEngageUnavailableWithoutAnEngager(t *testing.T) {
	m, cmd := apply(t, board(t, &fakeBackend{}), "e")
	if cmd != nil {
		t.Error("the engage key produced a command; there is nothing to acquire a lock with")
	}
	if m.engaged {
		t.Error("engaged is true with no Engager wired")
	}
	if m.flash == "" || m.flashOK {
		t.Errorf("flash = %q (ok=%v), want a refusal", m.flash, m.flashOK)
	}
}

// Toggling on acquires the lock, then fires an immediate poll — the same
// "check right away" shape Watch's own loop opens with.
func TestEngageToggleOnAcquiresLockAndPolls(t *testing.T) {
	eng := &fakeEngager{tickResult: EngageResult{LanesUsed: 0, LanesCap: 1}}
	m, cmd := apply(t, boardEngaged(t, &fakeBackend{}, eng), "e")
	if cmd == nil {
		t.Fatal("the engage key produced no command")
	}

	msg := cmd()
	lm, ok := msg.(lockMsg)
	if !ok || !lm.acquiring {
		t.Fatalf("cmd() = %T, want an acquiring lockMsg", msg)
	}
	if eng.acquires != 1 {
		t.Fatalf("AcquireLock called %d times, want 1", eng.acquires)
	}

	next, cmd2 := m.Update(lm)
	m = next.(Model)
	if !m.engaged {
		t.Fatal("engaged is false after a successful lock acquire")
	}

	tickMsg := cmd2()
	tm, ok := tickMsg.(engageTickMsg)
	if !ok {
		t.Fatalf("cmd2() = %T, want engageTickMsg", tickMsg)
	}
	if eng.ticks != 1 {
		t.Fatalf("Tick called %d times, want 1", eng.ticks)
	}

	next, _ = m.Update(tm)
	m = next.(Model)
	if m.tickResult.Dispatched {
		t.Error("tickResult.Dispatched is true, want the idle result the fake returned")
	}
	if m.nextPollAt.IsZero() {
		t.Error("nextPollAt was not set after a poll landed")
	}
	if !strings.Contains(m.View().Content, "engaged") {
		t.Errorf("header does not say engaged; screen was:\n%s", m.View().Content)
	}
}

// A poll that finds a winner reports it distinctly from idle, both in the
// model and in the flash.
func TestEngageTickReportingADispatchFlashesTheWinner(t *testing.T) {
	eng := &fakeEngager{}
	m := boardEngaged(t, &fakeBackend{}, eng)
	m.engaged = true

	next, _ := m.Update(engageTickMsg{
		at:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		res: EngageResult{Dispatched: true, Ticket: "WND-9", Verb: "run"},
	})
	m = next.(Model)

	if !m.tickResult.Dispatched {
		t.Fatal("tickResult.Dispatched is false, want true")
	}
	if m.tickResult.Ticket != "WND-9" || m.tickResult.Verb != "run" {
		t.Errorf("tickResult = %+v, want WND-9/run", m.tickResult)
	}
	if !m.flashOK || !strings.Contains(m.flash, "WND-9") {
		t.Errorf("flash = %q (ok=%v), want it to name WND-9", m.flash, m.flashOK)
	}
}

// A poll's error surfaces the same way a failed refresh does: on the flash,
// as bad news, without touching engaged itself — a transient Linear failure
// should not silently disengage the cockpit.
func TestEngageTickErrorFlashesAndStaysEngaged(t *testing.T) {
	eng := &fakeEngager{}
	m := boardEngaged(t, &fakeBackend{}, eng)
	m.engaged = true

	next, _ := m.Update(engageTickMsg{at: time.Now(), err: errors.New("linear is unreachable")})
	m = next.(Model)

	if !m.engaged {
		t.Error("a poll error disengaged the cockpit; it should only flash")
	}
	if m.flashOK || !strings.Contains(m.flash, "linear is unreachable") {
		t.Errorf("flash = %q (ok=%v), want it to report the poll error", m.flash, m.flashOK)
	}
}

// A poll landing after the cockpit has already disengaged must not
// resurrect engaged state or reschedule another poll.
func TestEngageTickAfterDisengageIsDropped(t *testing.T) {
	eng := &fakeEngager{}
	m := boardEngaged(t, &fakeBackend{}, eng)
	// engaged left false, as if toggle-off already landed.

	next, cmd := m.Update(engageTickMsg{at: time.Now(), res: EngageResult{Dispatched: true, Ticket: "WND-1"}})
	m = next.(Model)
	if m.engaged {
		t.Error("a stray tick re-engaged the cockpit")
	}
	if m.tickResult.Dispatched {
		t.Error("a stray tick's result was recorded after disengaging")
	}
	if cmd != nil {
		t.Error("a stray tick rescheduled another poll")
	}
}

// Toggling off releases the lock and clears the polling state immediately,
// so the header stops claiming a countdown even before the release lands.
func TestEngageToggleOffReleasesLock(t *testing.T) {
	eng := &fakeEngager{}
	m := boardEngaged(t, &fakeBackend{}, eng)
	m.engaged = true
	m.tickResult = EngageResult{Dispatched: true, Ticket: "WND-1", Verb: "run"}
	m.nextPollAt = time.Now().Add(time.Minute)

	m, cmd := apply(t, m, "e")
	if m.engaged {
		t.Error("engaged is still true right after toggling off")
	}
	if !m.nextPollAt.IsZero() {
		t.Error("nextPollAt was not cleared on toggle-off")
	}
	if cmd == nil {
		t.Fatal("toggle-off produced no command; the lock would never be released")
	}
	if _, ok := cmd().(lockMsg); !ok {
		t.Fatal("toggle-off did not produce a release lockMsg")
	}
	if eng.releases != 1 {
		t.Errorf("ReleaseLock called %d times, want 1", eng.releases)
	}
}

// --- rendered header states -------------------------------------------------

func TestEngageHeaderScreens(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	idle := boardEngaged(t, &fakeBackend{}, &fakeEngager{})
	idle.engaged = true
	idle.nextPollAt = at.Add(40 * time.Second)
	idle.now = at
	tuitest.AssertScreen(t, "board-engaged-idle", idle, "")

	dispatched := boardEngaged(t, &fakeBackend{}, &fakeEngager{})
	dispatched.engaged = true
	dispatched.tickResult = EngageResult{Dispatched: true, Ticket: "WND-9", Verb: "run"}
	dispatched.nextPollAt = at.Add(60 * time.Second)
	dispatched.now = at
	tuitest.AssertScreen(t, "board-engaged-dispatched", dispatched, "")
}

// Quitting while engaged must release the lock before the program actually
// exits — the one path the crash case (a killed terminal) does not cover,
// since lock.go's dead-holder reclaim only proves death, it does not race a
// graceful exit to be fast.
func TestEngageQuitReleasesTheLockBeforeQuitting(t *testing.T) {
	eng := &fakeEngager{}
	m := boardEngaged(t, &fakeBackend{}, eng)
	m.engaged = true

	m, cmd := apply(t, m, "q")
	if !m.quitting {
		t.Fatal("quitting is false after q while engaged")
	}
	if cmd == nil {
		t.Fatal("q while engaged produced no command; the lock would never be released")
	}
	msg := cmd()
	lm, ok := msg.(lockMsg)
	if !ok || lm.acquiring {
		t.Fatalf("cmd() = %T, want a releasing lockMsg", msg)
	}
	if eng.releases != 1 {
		t.Fatalf("ReleaseLock called %d times, want 1", eng.releases)
	}

	// Only once that release lands does the program actually quit.
	next, cmd2 := m.Update(lm)
	m = next.(Model)
	if cmd2 == nil {
		t.Fatal("no command after the release landed; the program never quits")
	}
	if _, ok := cmd2().(tea.QuitMsg); !ok {
		t.Errorf("cmd2() = %T, want tea.QuitMsg", cmd2())
	}
}

// ctrl+c (ForceQuit) goes through the same release-then-quit path as q.
func TestEngageForceQuitReleasesTheLock(t *testing.T) {
	eng := &fakeEngager{}
	m := boardEngaged(t, &fakeBackend{}, eng)
	m.engaged = true

	_, cmd := apply(t, m, "ctrl+c")
	if cmd == nil {
		t.Fatal("ctrl+c while engaged produced no command")
	}
	if _, ok := cmd().(lockMsg); !ok {
		t.Fatal("ctrl+c while engaged did not release the lock first")
	}
	if eng.releases != 1 {
		t.Errorf("ReleaseLock called %d times, want 1", eng.releases)
	}
}
