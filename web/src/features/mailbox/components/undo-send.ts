import { toast } from 'sonner';

import type { StoredComposeDraft } from '@/features/mailbox/components/compose-draft-store';

/**
 * Undo send: a written message waits a few seconds after Send, with an Undo action, before it
 * goes to the API. The composer closes at once and its text and files are held in memory, so Undo
 * (or a failure) reopens it exactly as written; the stored draft stays until the send succeeds.
 */

const DEFAULT_UNDO_SEND_MS = 5000;
let undoSendMs = DEFAULT_UNDO_SEND_MS;

/** How long a send can be undone; 0 sends at once. */
export function undoSendDelayMs(): number {
  return undoSendMs;
}

/** Sets the undo window (tests set 0 so a send runs at once). */
export function setUndoSendDelayMs(ms: number) {
  undoSendMs = Math.max(0, ms);
}

interface PendingSend {
  timer: ReturnType<typeof setTimeout>;
  toastId: string | number;
  run: () => Promise<unknown> | void;
}

/** Waiting sends by the draft key of the composer that made them. */
const pending = new Map<string, PendingSend>();

/** What a reopened composer starts with: what was written, the sender and any failure's cause. */
export interface ReopenedCompose {
  fromId: number;
  draft: StoredComposeDraft;
  files: readonly File[];
  failure?: unknown;
}

const reopened = new Map<string, ReopenedCompose>();

/** Called once as a composer opens; a draft key reopened by Undo or a failure returns its state. */
export function takeReopenedCompose(key: string): ReopenedCompose | undefined {
  const state = reopened.get(key);
  reopened.delete(key);
  return state;
}

export function markReopenedCompose(key: string, state: ReopenedCompose) {
  reopened.set(key, state);
}

// Leaving the page while a send waits would drop it: the browser asks first.
function warnBeforeUnload(event: BeforeUnloadEvent) {
  event.preventDefault();
}

/** Sends that left the undo window but have not been answered yet. */
let inFlight = 0;

function release() {
  if (pending.size === 0 && inFlight === 0) {
    window.removeEventListener('beforeunload', warnBeforeUnload);
  }
}

function settle(key: string) {
  pending.delete(key);
  release();
}

/** Runs a send; leaving the page stays guarded until it is answered. */
function launch(run: () => Promise<unknown> | void) {
  inFlight += 1;
  void Promise.resolve(run()).finally(() => {
    inFlight -= 1;
    release();
  });
}

/**
 * Sends the waiting message of `key` now. A composer opening on the same draft calls this first,
 * so the message cannot be shown and sent twice. Returns whether one was waiting.
 */
export function flushPendingSend(key: string): boolean {
  const entry = pending.get(key);
  if (!entry) return false;
  clearTimeout(entry.timer);
  toast.dismiss(entry.toastId);
  pending.delete(key);
  launch(entry.run);
  return true;
}

/**
 * Waits `delayMs`, showing `sending` with an Undo action, then calls `run`. Undo cancels and calls
 * `undo`. A second send for the same key sends the first at once.
 */
export function scheduleUndoableSend(
  key: string,
  {
    delayMs,
    sending,
    undoLabel,
    run,
    undo,
  }: {
    delayMs: number;
    sending: string;
    undoLabel: string;
    run: () => Promise<unknown> | void;
    undo: () => void;
  }
) {
  flushPendingSend(key);
  const toastId = toast(sending, {
    duration: delayMs,
    action: {
      label: undoLabel,
      onClick: () => {
        const entry = pending.get(key);
        if (!entry) return;
        clearTimeout(entry.timer);
        settle(key);
        undo();
      },
    },
  });
  const timer = setTimeout(() => {
    pending.delete(key);
    toast.dismiss(toastId);
    launch(run);
  }, delayMs);
  window.addEventListener('beforeunload', warnBeforeUnload);
  pending.set(key, { timer, toastId, run });
}
