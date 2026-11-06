import type { MessageDTO, SendMessageResult } from "@/shared/api/conversation-types";

const RECENT_COMPLETION_WINDOW_MS = 2 * 60_000;
const RECOVERY_TRACKING_TTL_MS = 30 * 60_000;
const MAX_TRACKED_RECOVERY_RUNS = 128;

export function resumeFallbackPollingDelay(attempt: number): number {
  return 15_000 * 2 ** Math.min(Math.max(attempt, 0), 2);
}

/** Observe pending identities; a short recency window also covers navigation/remount after completion. */
export class RecoveredGenerationTracker {
  private pending = new Map<string, number>();
  private completed = new Map<string, number>();

  private prune(records: Map<string, number>, now: number) {
    for (const [runID, timestamp] of records) {
      if (now - timestamp > RECOVERY_TRACKING_TTL_MS) records.delete(runID);
    }
    while (records.size > MAX_TRACKED_RECOVERY_RUNS) {
      const first = records.keys().next().value;
      if (first === undefined) break;
      records.delete(first);
    }
  }

  markCompleted(runID: string, now = Date.now()): boolean {
    this.prune(this.completed, now);
    if (this.completed.has(runID)) return false;
    this.completed.set(runID, now);
    this.pending.delete(runID);
    this.prune(this.completed, now);
    return true;
  }

  collect(messages: MessageDTO[], activeRunIDs?: ReadonlySet<string>, now = Date.now()): SendMessageResult[] {
    this.prune(this.pending, now);
    this.prune(this.completed, now);
    for (const message of messages) {
      const runID = message.runID?.trim();
      if (message.role === "assistant" && message.status === "pending" && runID) this.pending.set(runID, now);
    }
    this.prune(this.pending, now);
    const results: SendMessageResult[] = [];
    for (const assistantMessage of messages) {
      const runID = assistantMessage.runID?.trim();
      const completedAt = Date.parse(assistantMessage.updatedAt || assistantMessage.createdAt);
      const recent = Number.isFinite(completedAt) && completedAt <= now && now - completedAt <= RECENT_COMPLETION_WINDOW_MS;
      if (assistantMessage.role !== "assistant" || assistantMessage.status !== "success" || !runID ||
        activeRunIDs?.has(runID) || (!this.pending.has(runID) && !recent)) continue;
      const userMessage = messages.find((message) => message.role === "user" && message.publicID === assistantMessage.parentPublicID);
      if (userMessage && this.markCompleted(runID, now)) results.push({ userMessage, assistantMessage });
    }
    return results;
  }
}

function waitForRecovery(signal: AbortSignal, delayMS = 750): Promise<void> {
  signal.throwIfAborted();
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { signal.removeEventListener("abort", abort); resolve(); }, delayMS);
    const abort = () => { clearTimeout(timer); reject(new DOMException("Aborted", "AbortError")); };
    signal.addEventListener("abort", abort, { once: true });
  });
}

/** Retry only GET resume connections, then leave persisted polling/foreground return in control. */
export async function resumeWithRecovery({ signal, attempt, isPermanentFailure, onFailure, pause = waitForRecovery }: {
  signal: AbortSignal;
  attempt: () => Promise<void>;
  isPermanentFailure: (error: unknown) => boolean;
  onFailure: (error: unknown) => void;
  pause?: (signal: AbortSignal, delayMS: number) => Promise<void>;
}): Promise<"completed" | "exhausted" | "stopped"> {
  const retryDelays = [1000, 2000, 4000];
  for (let attemptIndex = 0; ; attemptIndex += 1) {
    if (signal.aborted) return "stopped";
    try {
      await attempt();
      return signal.aborted ? "stopped" : "completed";
    } catch (error) {
      if (signal.aborted) return "stopped";
      onFailure(error);
      if (isPermanentFailure(error)) return "stopped";
      const delay = retryDelays[attemptIndex];
      if (delay === undefined) return "exhausted";
      try { await pause(signal, delay); } catch { return "stopped"; }
    }
  }
}

export type SubmissionRecovery =
  | { kind: "completed"; result: SendMessageResult }
  | { kind: "detached" }
  | { kind: "unconfirmed" };

export class SubmissionRecoveryError extends Error {
  readonly recovery: Exclude<SubmissionRecovery, { kind: "completed" }>;
  constructor(recovery: Exclude<SubmissionRecovery, { kind: "completed" }>, options?: ErrorOptions) {
    super("Generation result is not yet available", options);
    this.name = "SubmissionRecoveryError";
    this.recovery = recovery;
  }
}

/** Resolve only through the existing run identity. A missing response never authorizes another generation. */
export async function recoverSubmittedRun({
  accepted,
  signal,
  queryStatus,
  readResult,
  isTerminalError,
  pause = waitForRecovery,
}: {
  accepted: boolean;
  signal: AbortSignal;
  queryStatus: (signal: AbortSignal) => Promise<string | undefined>;
  readResult: (signal: AbortSignal) => Promise<SendMessageResult | null>;
  isTerminalError: (error: unknown) => boolean;
  pause?: (signal: AbortSignal) => Promise<void>;
}): Promise<SubmissionRecovery> {
  let knownAccepted = accepted;
  let missing = false;
  let terminalSeen = false;
  for (let attempt = 0; attempt < 3; attempt += 1) {
    signal.throwIfAborted();
    // Bound read-only recovery as well as the retry count, even when fetch stalls.
    const querySignal = AbortSignal.any([signal, AbortSignal.timeout(2500)]);
    let status: string | undefined;
    try {
      status = (await queryStatus(querySignal))?.trim().toLowerCase();
      missing = !status;
    } catch {
      signal.throwIfAborted();
      continue;
    }
    if (status) {
      knownAccepted = true;
      if (status === "pending" || status === "running" || status === "queued") {
        return { kind: "detached" };
      }
      terminalSeen = true;
      if (["success", "error", "interrupted", "canceled", "cancelled", "blocked"].includes(status)) {
        // The resume snapshot contains the authoritative payload. An unavailable
        // snapshot is still uncertainty, even when the status says it finished.
        try {
          const result = await readResult(querySignal);
          if (result) return { kind: "completed", result };
        } catch (error) {
          signal.throwIfAborted();
          if (isTerminalError(error)) throw error;
        }
      }
    }
    if (attempt < 2) await pause(signal);
  }
  return { kind: knownAccepted && !missing && !terminalSeen ? "detached" : "unconfirmed" };
}
