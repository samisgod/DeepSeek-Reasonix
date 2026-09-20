import type { projectConversation } from "../app-runtime/conversationProjection";
import type { SessionDraftSurface } from "../app-runtime/useSessionDraftSurface";

type ConversationStatus = ReturnType<typeof projectConversation>["status"];

const DRAFT_ATTENTION_PHASES = new Set([
  "accepted",
  "dispatch_unknown",
  "dispatching_shell",
  "resume_required",
  "runtime_failed",
  "terminal_failed",
]);

/** Healthy drafts use the new-session landing; recovery controls need room. */
export function draftSurfaceNeedsAttention(draft: SessionDraftSurface): boolean {
  return draft.saveState === "conflict" || draft.saveState === "error" || Boolean(draft.taskError)
    || Boolean(draft.operation && DRAFT_ATTENTION_PHASES.has(draft.operation.phase));
}

export function creationHeroVisible(draft: SessionDraftSurface | null | undefined, emptyHero: boolean): boolean {
  // A hidden formal tab must not collapse the active draft's recovery surface.
  return draft ? !draftSurfaceNeedsAttention(draft) : emptyHero;
}

/**
 * Shell status identity for an active draft. The bottom status bar belongs to
 * the shell, not to a session, so it stays visible on the new-session landing
 * surface — the only surface a portable install has before its first turn.
 * A draft owns the workspace it will open, but it has no session yet: the
 * formal session it replaced must not leak its branch, turn count, or token and
 * cost telemetry into that bar.
 */
export function draftStatusBase(base: ConversationStatus, draft: SessionDraftSurface): ConversationStatus {
  const projectScoped = draft.draft.scope === "project";
  const workspaceRoot = projectScoped ? (draft.draft.workspaceRoot ?? "").trim() : "";
  return {
    ...base,
    context: { used: 0, window: 0, sessionTokens: 0 },
    usage: undefined,
    sessionTokens: 0,
    turnTokens: 0,
    lastTurnOutputTokens: 0,
    lastTurnModelMs: 0,
    lastTurnOutputEstimated: false,
    lastRequestTps: undefined,
    turnCost: 0,
    turnRateBand: undefined,
    cost: 0,
    workspacePath: workspaceRoot,
    workspaceName: projectScoped
      ? workspaceRoot.replace(/[\\/]+$/, "").split(/[\\/]/).filter(Boolean).pop()
      : undefined,
    gitBranch: undefined,
  };
}
