import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const toast = vi.hoisted(() =>
  Object.assign(
    vi.fn<(text: string, options?: unknown) => string>(() => 'toast-1'),
    { dismiss: vi.fn() }
  )
);
vi.mock('sonner', () => ({ toast }));

import {
  flushPendingSend,
  markReopenedCompose,
  scheduleUndoableSend,
  takeReopenedCompose,
} from '@/features/mailbox/components/undo-send';

type ToastOptions = { duration: number; action: { label: string; onClick: () => void } };

function schedule(key = 'draft') {
  const run = vi.fn();
  const undo = vi.fn();
  scheduleUndoableSend(key, { delayMs: 5000, sending: 'Sending…', undoLabel: 'Undo', run, undo });
  const options = toast.mock.calls.at(-1)?.[1] as unknown as ToastOptions;
  return { run, undo, options };
}

describe('undo send', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    toast.mockClear();
    toast.dismiss.mockClear();
  });
  afterEach(() => {
    flushPendingSend('draft');
    flushPendingSend('other');
    vi.useRealTimers();
  });

  it('sends once the undo window ends', () => {
    const { run, undo, options } = schedule();
    expect(toast).toHaveBeenCalledWith('Sending…', expect.objectContaining({ duration: 5000 }));
    expect(options.action.label).toBe('Undo');
    vi.advanceTimersByTime(4999);
    expect(run).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(run).toHaveBeenCalledOnce();
    expect(undo).not.toHaveBeenCalled();
    expect(toast.dismiss).toHaveBeenCalledWith('toast-1');
  });

  it('Undo cancels the send and reopens', () => {
    const { run, undo, options } = schedule();
    options.action.onClick();
    vi.advanceTimersByTime(10_000);
    expect(undo).toHaveBeenCalledOnce();
    expect(run).not.toHaveBeenCalled();
    // A late second click does nothing.
    options.action.onClick();
    expect(undo).toHaveBeenCalledOnce();
  });

  it('a composer opening on the same draft sends the waiting message at once', () => {
    const { run } = schedule();
    expect(flushPendingSend('other')).toBe(false);
    expect(flushPendingSend('draft')).toBe(true);
    expect(run).toHaveBeenCalledOnce();
    vi.advanceTimersByTime(10_000);
    expect(run).toHaveBeenCalledOnce();
  });

  it('asks before the page unloads while a send waits', () => {
    schedule();
    const waiting = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(waiting);
    expect(waiting.defaultPrevented).toBe(true);
    vi.advanceTimersByTime(5000);
    const idle = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(idle);
    expect(idle.defaultPrevented).toBe(false);
  });

  it('hands the reopened state to the next composer once', () => {
    markReopenedCompose('draft', { fromId: 7 });
    expect(takeReopenedCompose('draft')).toEqual({ fromId: 7 });
    expect(takeReopenedCompose('draft')).toBeUndefined();
  });
});
