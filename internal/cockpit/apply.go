package cockpit

import (
	"context"
	"fmt"
	"strings"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
)

// Apply performs one judgment.
//
// # This is the one write path in wand that does not consult the guard
//
// Every other status write — the lifecycle verbs, the PreToolUse hook —
// runs through guard.CheckState first, and four of the six dispositions
// here are transitions that function refuses: Todo, Scoping, Canceled,
// Duplicate. The absence of the call is deliberate and is the point of the
// package.
//
// The guard's subject is an *agent acting unattended*. Todo is refused
// there because a bot that promotes its own ticket has granted itself
// authorization; Canceled is refused because a bot that closes a ticket has
// revoked a human's. Neither sentence is true of a person sitting at a
// terminal pressing a key on a screen that just told them what the
// transition means. The guard is not a lock on the statuses, it is a lock on
// who may reach them, and this is the door with a human on the other side.
//
// Two structural things keep that claim honest, and both are load-bearing:
//
//   - Nothing calls Apply except the interactive screen, on a keystroke,
//     after a confirmation step that names what the transition authorizes.
//   - `wand ui --dump-screen` — the one way an agent can drive this
//     interface — is wired with no writer at all, so the keys are inert
//     there. An agent cannot reach this function by scripting the screen it
//     uses to look at the screen.
//
// # Ordering
//
// Two of the dispositions need a write before the status write, for the
// same reason handback posts its question first: the second write is the
// one that ends the ticket's visibility, so anything a reader will later
// need has to already be on it.
//
//   - Canceled posts the reason, then moves. A crash between them leaves an
//     open ticket carrying its own argument for closure, which a person can
//     finish. The reverse leaves a closed ticket nobody can explain.
//   - Duplicate writes the relation, then moves. Same shape: a Duplicate
//     status that names nothing is a dead end.
//
// Everything checkable without writing is checked first, so a refusal
// leaves nothing behind.
//
// # Retrying
//
// Neither pre-write is undoable, and the screen deliberately keeps a failed
// judgment on the confirmation so the person can fix it and press enter
// again. That makes the retry the dangerous path rather than the safe one:
// running Apply from the top a second time would post the reason twice, and
// would re-issue a relation Linear has already recorded and now refuses —
// which would leave the duplicate permanently uncompletable through this
// screen, the one outcome worse than the failure being retried.
//
// So Apply returns the intent it got, with [Progress] marking what landed,
// and skips a pre-write that is already marked. The caller stores the
// returned intent and hands that one back on the retry; nothing else is
// required of it. On the first call the field is zero, which is the same
// thing as "nothing has been written yet".
func Apply(ctx context.Context, cl Linear, cov covenant.Covenant, in Intent) (Intent, error) {
	if ok, why := in.Ready(); !ok {
		return in, fmt.Errorf("%s: %s", in.Disp.Name, why)
	}

	// Resolve the destination before anything is written. A board that has
	// drifted from the covenant, or a covenant missing the status, stops
	// the whole act here.
	stateID, err := resolveState(ctx, cl, cov, in.Issue.TeamID, in.Disp.Status)
	if err != nil {
		return in, err
	}

	if !in.Done.PreWritten {
		switch in.Disp.Field {
		case FieldIdentifier:
			canonical, err := cl.IssueByIdentifier(ctx, strings.TrimSpace(in.Text))
			if err != nil {
				return in, fmt.Errorf("looking up %s: %w", strings.TrimSpace(in.Text), err)
			}
			if canonical.ID == in.Issue.ID {
				return in, fmt.Errorf("%s cannot duplicate itself", in.Issue.Identifier)
			}
			if err := cl.CreateRelation(ctx, in.Issue.ID, canonical.ID, linear.RelationDuplicate); err != nil {
				return in, err
			}
			in.Done.PreWritten = true
		case FieldReason:
			if err := cl.CreateComment(ctx, in.Issue.ID, strings.TrimSpace(in.Text)); err != nil {
				return in, err
			}
			in.Done.PreWritten = true
		}
	}

	update := linear.IssueUpdate{StateID: stateID}
	switch in.Disp {
	case ToBacklog:
		p := in.Priority
		update.Priority = &p
	case ToBacklogUnranked:
		// An explicit 0. Leaving the field alone would carry whatever
		// priority the ticket already had into the pool, which is the
		// opposite of the judgment this disposition records.
		none := 0
		update.Priority = &none
	}
	return in, cl.UpdateIssue(ctx, in.Issue.ID, update)
}

// resolveState turns a covenant status key into the team's workflow state
// UUID. Deliberately not verbs.resolveState: that one runs the guard, which
// is exactly the difference between this package and every other writer.
func resolveState(ctx context.Context, cl Linear, cov covenant.Covenant, teamID, key string) (string, error) {
	name := cov.StatusName(key)
	if name == "" {
		return "", fmt.Errorf("the covenant has no %q status; this is a wand bug", key)
	}
	states, err := cl.TeamStates(ctx, teamID)
	if err != nil {
		return "", err
	}
	if id, ok := linear.StateIDByName(states, name); ok {
		return id, nil
	}
	return "", fmt.Errorf(
		"the team has no status named %q; the board has drifted from the covenant — run `wand doctor`, and `wand init` to repair", name)
}
