package agent

import (
	"context"
	"fmt"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
)

// The progress budget owns the host's adaptive progress checkpoint: when the
// active todo produces no new host-observed work for a configured number of
// tool-call rounds, the host asks the model to reassess once. The checkpoint is
// user-owned — the round count is configured (see NormalizeProgressBudgetRounds)
// and can be turned off entirely — and it never ends a run.

// todoProgressNudgeRounds is the first adaptive checkpoint. The host asks the
// model to reassess, but keeps the turn alive so it can recover. It is the
// built-in default for the user-configurable progress budget (see
// NormalizeProgressBudgetRounds).
const todoProgressNudgeRounds = 8

// Bounds for the user-configurable progress budget. ProgressBudgetRoundsMin is
// low enough to still catch a grind within one tool batch; the max keeps the
// checkpoint from degrading into "never".
const (
	// ProgressBudgetRoundsMin is the smallest accepted reassessment round.
	ProgressBudgetRoundsMin = 3
	// ProgressBudgetRoundsMax is the largest accepted reassessment round.
	ProgressBudgetRoundsMax = 64
	// ProgressBudgetRoundsOff disables the reassessment checkpoint and its
	// Goal-only redirect entirely.
	ProgressBudgetRoundsOff = -1
)

// DefaultProgressBudgetRounds is the round count used when configuration leaves
// the progress budget unset. Exported so hosts (CLI status, Desktop settings)
// render the same number the loop enforces.
const DefaultProgressBudgetRounds = todoProgressNudgeRounds

// NormalizeProgressBudgetRounds resolves a configured round count into the
// checkpoint the loop enforces: 0 means the built-in default, a negative value
// turns the checkpoint off, and anything else is clamped into range.
func NormalizeProgressBudgetRounds(rounds int) int {
	switch {
	case rounds == 0:
		return DefaultProgressBudgetRounds
	case rounds < 0:
		return ProgressBudgetRoundsOff
	case rounds < ProgressBudgetRoundsMin:
		return ProgressBudgetRoundsMin
	case rounds > ProgressBudgetRoundsMax:
		return ProgressBudgetRoundsMax
	default:
		return rounds
	}
}

// progressRedirectRounds is the Goal-only second checkpoint: after this many
// zero-evidence rounds the host asks for a new plan instead of a reassessment.
func progressRedirectRounds(nudgeRounds int) int {
	return nudgeRounds * 2
}

// todoProgressNudgeMessage is the first checkpoint's model-facing text. The
// "Host progress check:" prefix is a persisted host-message marker (see
// hostProgressMessagePrefixes), so it must stay byte-stable.
func todoProgressNudgeMessage(rounds int) string {
	return fmt.Sprintf("Host progress check: the current todo has produced no new completion, unique read, command, or mutation for %d tool-call rounds. Reassess before using more tools: sign off the current item if it is done, narrow the remaining work without replacing the active item, or explain/ask about a real blocker. Do not repeat reads, commands, or writes just to reset this guard.", rounds)
}

// todoProgressRedirectMessage is the Goal-only second checkpoint's text. Like
// the nudge it is a persisted marker ("Host progress redirect:") and must stay
// byte-stable.
func todoProgressRedirectMessage(rounds int) string {
	return fmt.Sprintf("Host progress redirect: the current todo still has no new completion or unique host-observed work after %d tool-call rounds. Re-plan and continue: shrink the active step, switch tools or approach, delegate a focused sub-task, or use update_goal(blocked) only if a user or external condition is the sole blocker. Do not repeat the same calls.", rounds)
}

func loopGuardNoticeText() string {
	return i18n.M.LoopGuard
}

// canonicalTodoProgress returns the number of completed canonical todos and
// whether any item is still incomplete. The count tracks real progress only:
// title rewording and pending-list churn do not change it.
func (a *Agent) canonicalTodoProgress() (int, bool) {
	a.sess.todoMu.Lock()
	defer a.sess.todoMu.Unlock()
	completed := 0
	incomplete := false
	for _, todo := range a.sess.todoState {
		if canonicalTodoStatus(todo.Status) == "completed" {
			completed++
		} else {
			incomplete = true
		}
	}
	return completed, incomplete
}

// trackTodoProgress advances the stall streak and asks the model to reassess
// once, at the checkpoint. It never ends a run: a stall only ever produces one
// host message per checkpoint, and the user's configured round budget (or the
// off sentinel) decides when that happens.
//
// The checkpoint is user-owned: a configured round budget replaces the built-in
// one, and ProgressBudgetRoundsOff skips both the nudge and the Goal-only
// redirect below, so a user who raises or clears it is never second-guessed by
// a hardcoded number.
func (a *Agent) trackTodoProgress(ctx context.Context, state *turnRuntime, receiptMark int) error {
	if a.planMode.Load() {
		return nil
	}
	nudgeRounds := a.progressBudgetRounds
	if nudgeRounds <= 0 {
		return nil
	}
	nextProgress, nextTracking := a.canonicalTodoProgress()
	hostProgress := false
	if a.task.ledger != nil {
		for _, sig := range a.task.ledger.SuccessfulProgressSignaturesSince(receiptMark) {
			if _, seen := state.seenTodoProgress[sig]; !seen {
				hostProgress = true
				state.seenTodoProgress[sig] = struct{}{}
			}
		}
	}
	switch {
	case !nextTracking, !state.trackingTodoProgress || nextProgress > state.todoProgress || hostProgress:
		state.todoStallRounds = 0
	default:
		state.todoStallRounds++
	}
	state.todoProgress, state.trackingTodoProgress = nextProgress, nextTracking
	if !a.hostContinuationEnabled(ctx) {
		return nil
	}
	if state.todoStallRounds == nudgeRounds {
		if err := a.appendCommittedMessages(ctx, "todo-progress-check", HostGeneratedUserMessage(a.withTurnPreferences(todoProgressNudgeMessage(state.todoStallRounds)))); err != nil {
			return err
		}
		a.svc.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Code: event.NoticeCodeLoopGuard,
			Text: loopGuardNoticeText(), Detail: fmt.Sprintf("the current todo has no new completion, unique read, command, or mutation for %d consecutive tool-call rounds; asking the assistant to reassess", state.todoStallRounds)})
	}
	if state.todoStallRounds < progressRedirectRounds(nudgeRounds) {
		return nil
	}
	if _, goalScoped := DeliveryExecutionScopeFromContext(ctx); goalScoped {
		rounds := state.todoStallRounds
		state.todoStallRounds = 0
		if err := a.appendCommittedMessages(ctx, "todo-progress-redirect", HostGeneratedUserMessage(a.withTurnPreferences(todoProgressRedirectMessage(rounds)))); err != nil {
			return err
		}
		a.svc.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Code: event.NoticeCodeLoopGuard,
			Text: loopGuardNoticeText(), Detail: fmt.Sprintf("the current Goal todo made no host-observed progress for %d rounds; resetting the intervention epoch and requiring a new plan", rounds)})
	}
	return nil
}
