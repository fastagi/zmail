'use client';

import { useCallback, useEffect, useRef } from 'react';
import { toast } from 'sonner';

/** Default undo window in milliseconds before a delayed commit fires. */
export const DEFAULT_UNDO_WINDOW_MS = 5000;

/** Common instant-response mail actions supported by optimistic undo. */
export type MailUndoActionKind =
  | 'archive'
  | 'delete'
  | 'markRead'
  | 'markUnread'
  | 'star'
  | 'send';

export interface OptimisticUndoActionOptions<TResult = unknown> {
  /** Optional semantic action category. */
  kind?: MailUndoActionKind;
  /** Toast copy displayed immediately after `optimisticApply()` runs. */
  message: string;
  /** Label for the Undo button inside the toast. Defaults to `"Undo"`. */
  undoLabel?: string;
  /** Error toast copy shown if `commit()` rejects. */
  errorMessage?: string | ((error: unknown) => string);
  /** Optional confirmation toast copy shown when the user clicks Undo. */
  undoneMessage?: string;
  /** Undo window in milliseconds before `commit()` runs. Defaults to `5000`. */
  undoWindowMs?: number;
  /**
   * When `true`, invokes `commit()` immediately after `optimisticApply()` instead of
   * waiting for the undo window to expire. Clicking Undo still calls `rollback()`.
   */
  immediateCommit?: boolean;
  /** Runs synchronously for 0ms UI feedback. */
  optimisticApply: () => void;
  /** Reverts the optimistic UI change on Undo or commit failure. */
  rollback: () => void;
  /** Persists the action asynchronously. */
  commit: () => Promise<TResult>;
  /** Called if `commit()` resolves and the action was not undone. */
  onCommitted?: (result: TResult) => void;
  /** Called after the user clicks Undo and `rollback()` runs. */
  onUndone?: () => void;
  /** Called after `commit()` rejects and `rollback()` runs. */
  onError?: (error: unknown) => void;
}

export interface UseOptimisticUndoOptions {
  /** Default undo window in milliseconds (defaults to `5000`). */
  defaultUndoWindowMs?: number;
  /** Default Undo button label (defaults to `"Undo"`). */
  defaultUndoLabel?: string;
  /** Default error toast message when `commit()` rejects. */
  defaultErrorMessage?: string;
  /**
   * How to handle pending delayed commits when the owning component unmounts:
   * - `'cancel'` (default): clear pending timers and dismiss toasts without committing.
   * - `'flush'`: immediately run pending commits on unmount.
   */
  onUnmount?: 'cancel' | 'flush';
}

export interface OptimisticUndoHandle {
  /** Cancels the pending commit (if still within the window) and rolls back. */
  undo: () => boolean;
  /** Immediately executes the pending commit ahead of the timer deadline. */
  flush: () => boolean;
  /** The `sonner` toast identifier for this action. */
  toastId: string | number;
}

interface PendingActionEntry {
  toastId: string | number;
  timer: ReturnType<typeof setTimeout> | null;
  flush: () => boolean;
  cancel: () => void;
}

const DEFAULT_UNDO_LABEL = 'Undo';
const DEFAULT_ERROR_MESSAGE = 'Action failed. Changes were reverted.';

/**
 * Provides instant-response (0ms) optimistic mail actions backed by a `sonner` Undo toast.
 *
 * - Calls `optimisticApply()` synchronously.
 * - Shows a `sonner` toast with an Undo button for `undoWindowMs` (default 5000ms).
 * - Clicking Undo within the window cancels the pending commit timer and calls `rollback()`.
 * - After the window (or immediately when `immediateCommit: true`), runs `commit()`; if it
 *   rejects, automatically calls `rollback()` and shows an error toast.
 * - Cleans up (cancels or flushes) pending timers on unmount.
 */
export function useOptimisticUndo(hookOptions: UseOptimisticUndoOptions = {}) {
  const {
    defaultUndoWindowMs = DEFAULT_UNDO_WINDOW_MS,
    defaultUndoLabel = DEFAULT_UNDO_LABEL,
    defaultErrorMessage = DEFAULT_ERROR_MESSAGE,
    onUnmount = 'cancel',
  } = hookOptions;

  const onUnmountRef = useRef(onUnmount);
  useEffect(() => {
    onUnmountRef.current = onUnmount;
  }, [onUnmount]);

  const nextIdRef = useRef(0);
  const pendingRef = useRef<Map<number, PendingActionEntry>>(new Map());

  const cancelAll = useCallback(() => {
    const entries = Array.from(pendingRef.current.values());
    pendingRef.current.clear();
    for (const entry of entries) {
      entry.cancel();
    }
  }, []);

  const flushAll = useCallback(() => {
    const entries = Array.from(pendingRef.current.values());
    pendingRef.current.clear();
    for (const entry of entries) {
      entry.flush();
    }
  }, []);

  useEffect(() => {
    const pending = pendingRef.current;
    return () => {
      const entries = Array.from(pending.values());
      pending.clear();
      const mode = onUnmountRef.current;
      for (const entry of entries) {
        if (mode === 'flush') {
          entry.flush();
        } else {
          entry.cancel();
        }
      }
    };
  }, []);

  const execute = useCallback(
    <TResult = unknown>(options: OptimisticUndoActionOptions<TResult>): OptimisticUndoHandle => {
      const entryId = ++nextIdRef.current;
      const undoWindowMs = Math.max(0, options.undoWindowMs ?? defaultUndoWindowMs);
      const undoLabel = options.undoLabel ?? defaultUndoLabel;
      const immediateCommit = options.immediateCommit === true;

      let committed = false;
      let undone = false;
      let cancelled = false;

      // 1. Apply optimistic state synchronously for 0ms UI feedback.
      options.optimisticApply();

      const clearTimer = () => {
        const current = pendingRef.current.get(entryId);
        if (current && current.timer !== null) {
          clearTimeout(current.timer);
          current.timer = null;
        }
      };

      const resolveErrorText = (error: unknown): string => {
        if (typeof options.errorMessage === 'function') {
          return options.errorMessage(error);
        }
        return options.errorMessage ?? defaultErrorMessage;
      };

      const handleCommitError = (error: unknown) => {
        if (undone) return;
        toast.dismiss(toastId);
        options.rollback();
        toast.error(resolveErrorText(error));
        options.onError?.(error);
      };

      const runCommit = (dismissToast: boolean): boolean => {
        if (committed || undone || cancelled) return false;
        committed = true;
        clearTimer();
        pendingRef.current.delete(entryId);
        if (dismissToast) {
          toast.dismiss(toastId);
        }

        let commitPromise: Promise<TResult>;
        try {
          commitPromise = Promise.resolve(options.commit());
        } catch (error) {
          handleCommitError(error);
          return true;
        }

        commitPromise.then(
          result => {
            if (!undone) {
              options.onCommitted?.(result);
            }
          },
          (error: unknown) => {
            handleCommitError(error);
          }
        );
        return true;
      };

      const handleUndo = (): boolean => {
        if (undone || cancelled) return false;
        if (!immediateCommit && committed) return false;
        undone = true;
        clearTimer();
        pendingRef.current.delete(entryId);
        toast.dismiss(toastId);
        options.rollback();
        if (options.undoneMessage) {
          toast(options.undoneMessage);
        }
        options.onUndone?.();
        return true;
      };

      const handleCancel = () => {
        if (cancelled) return;
        cancelled = true;
        clearTimer();
        pendingRef.current.delete(entryId);
        toast.dismiss(toastId);
      };

      // 2. Show toast with Undo action button.
      const toastId = toast(options.message, {
        duration: undoWindowMs > 0 ? undoWindowMs : defaultUndoWindowMs,
        action: {
          label: undoLabel,
          onClick: () => {
            handleUndo();
          },
        },
      });

      const entry: PendingActionEntry = {
        toastId,
        timer: null,
        flush: () => runCommit(true),
        cancel: handleCancel,
      };

      // 3. Either commit immediately or schedule commit after the undo window.
      if (immediateCommit) {
        void runCommit(false);
      } else {
        entry.timer = setTimeout(() => {
          entry.timer = null;
          void runCommit(true);
        }, undoWindowMs);
        pendingRef.current.set(entryId, entry);
      }

      return {
        undo: handleUndo,
        flush: () => runCommit(true),
        toastId,
      };
    },
    [defaultUndoWindowMs, defaultUndoLabel, defaultErrorMessage]
  );

  return {
    execute,
    flushAll,
    cancelAll,
  };
}
