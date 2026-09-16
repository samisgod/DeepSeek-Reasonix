package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"

	_ "reasonix/internal/tool/builtin"
)

// The progress budget is the host's adaptive checkpoint: after a configured
// number of tool-call rounds without new host-observed work on the active todo,
// the host asks the model to reassess once, and a Goal-scoped run gets one
// re-plan redirect at twice the threshold. These tests pin the user-facing
// contract: the configured round count is what the loop enforces, off means
// silent, ordinary chat never carries the continuation, and unique host work
// renews the lease.

func progressBudgetTodoTurn() testutil.Turn {
	return testutil.Turn{ToolCalls: []provider.ToolCall{{
		ID: "todo", Name: "todo_write",
		Arguments: `{"todos":[{"content":"finish the task","status":"in_progress"}]}`,
	}}}
}

func progressBudgetReadTurn(id, path string) testutil.Turn {
	return testutil.Turn{ToolCalls: []provider.ToolCall{{
		ID: id, Name: "inspect", Arguments: fmt.Sprintf(`{"path":%q}`, path),
	}}}
}

// progressBudgetTurns scripts one todo write, then repeated identical reads of
// path. The first read renews the lease; exact repeats after it accumulate
// stall rounds.
func progressBudgetTurns(repeats int, path string) []testutil.Turn {
	turns := []testutil.Turn{progressBudgetTodoTurn(), progressBudgetReadTurn("read-first", path)}
	for i := range repeats {
		turns = append(turns, progressBudgetReadTurn(fmt.Sprintf("read-%d", i), path))
	}
	return append(turns, testutil.Turn{Text: "Done."})
}

func progressBudgetAgent(t *testing.T, opts Options, turns []testutil.Turn) (*Agent, *testutil.MockProvider) {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "inspect", readOnly: true})
	reg.Add(mustBuiltinTool(t, "todo_write"))
	mp := testutil.NewMock("m", turns...)
	return New(mp, reg, NewSession(""), opts, event.Discard), mp
}

func modelHistoryContains(a *Agent, sub string) bool {
	for _, msg := range a.ModelHistorySnapshot() {
		if strings.Contains(msg.Content, sub) {
			return true
		}
	}
	return false
}

func TestProgressBudgetNudgeUsesConfiguredRounds(t *testing.T) {
	for _, tc := range []struct {
		name           string
		repeats        int
		wantProgressCh bool
	}{
		{"below threshold stays silent", 2, false},
		{"at threshold nudges", 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := progressBudgetAgent(t,
				Options{ContinuationPolicy: ContinuationExplicitFlow, ProgressBudgetRounds: 3},
				progressBudgetTurns(tc.repeats, "same"))
			if err := a.Run(context.Background(), "work until the todo is complete"); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := modelHistoryContains(a, "Host progress check"); got != tc.wantProgressCh {
				t.Fatalf("nudge present = %v, want %v (repeats=%d, budget=3)", got, tc.wantProgressCh, tc.repeats)
			}
		})
	}
}

func TestProgressBudgetOffStaysSilent(t *testing.T) {
	a, _ := progressBudgetAgent(t,
		Options{ContinuationPolicy: ContinuationExplicitFlow, ProgressBudgetRounds: ProgressBudgetRoundsOff},
		progressBudgetTurns(12, "same"))
	if err := a.Run(context.Background(), "work until the todo is complete"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if modelHistoryContains(a, "Host progress check") {
		t.Fatal("progress budget off must not inject a reassessment nudge")
	}
}

func TestProgressBudgetOrdinaryChatStaysSilent(t *testing.T) {
	a, _ := progressBudgetAgent(t, Options{}, progressBudgetTurns(12, "same"))
	if err := a.Run(context.Background(), "work until the todo is complete"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if modelHistoryContains(a, "Host progress check") {
		t.Fatal("ordinary chat must not inject a todo stall continuation")
	}
}

func TestProgressBudgetGoalRedirectsAtDoubleRounds(t *testing.T) {
	a, _ := progressBudgetAgent(t,
		Options{ContinuationPolicy: ContinuationExplicitFlow, ProgressBudgetRounds: 3},
		progressBudgetTurns(7, "same"))
	ctx := WithDeliveryExecutionScope(context.Background(), DeliveryExecutionScope{ID: "goal-1", TaskText: "finish the task"})
	if err := a.Run(ctx, "work until the todo is complete"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !modelHistoryContains(a, "Host progress check") {
		t.Fatal("the first checkpoint nudge went missing before the Goal redirect")
	}
	if !modelHistoryContains(a, "Host progress redirect") {
		t.Fatal("a stalled Goal todo must be asked to re-plan at twice the nudge threshold")
	}
}

func TestProgressBudgetRenewsOnUniqueHostWork(t *testing.T) {
	turns := []testutil.Turn{progressBudgetTodoTurn(), progressBudgetReadTurn("read-first", "same")}
	// Two stall rounds, then a unique read of another path resets the streak;
	// the repeats after it must not reach the threshold from the earlier count.
	turns = append(turns,
		progressBudgetReadTurn("stall-1", "same"),
		progressBudgetReadTurn("stall-2", "same"),
		progressBudgetReadTurn("unique", "other"),
		progressBudgetReadTurn("after-1", "same"),
		progressBudgetReadTurn("after-2", "same"),
		testutil.Turn{Text: "Done."},
	)
	a, _ := progressBudgetAgent(t,
		Options{ContinuationPolicy: ContinuationExplicitFlow, ProgressBudgetRounds: 3}, turns)
	if err := a.Run(context.Background(), "work until the todo is complete"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if modelHistoryContains(a, "Host progress check") {
		t.Fatal("unique host work should renew the progress lease before the nudge threshold")
	}
}

func TestNormalizeProgressBudgetRounds(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want int
	}{
		{0, DefaultProgressBudgetRounds},
		{-5, ProgressBudgetRoundsOff},
		{1, ProgressBudgetRoundsMin},
		{3, 3},
		{17, 17},
		{ProgressBudgetRoundsMax, ProgressBudgetRoundsMax},
		{1000, ProgressBudgetRoundsMax},
	} {
		if got := NormalizeProgressBudgetRounds(tc.in); got != tc.want {
			t.Errorf("NormalizeProgressBudgetRounds(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
	if DefaultProgressBudgetRounds != 8 {
		t.Errorf("default progress budget = %d, want the historical 8", DefaultProgressBudgetRounds)
	}
	if progressRedirectRounds(8) != 16 {
		t.Errorf("goal redirect at default = %d, want the historical 16", progressRedirectRounds(8))
	}
}
