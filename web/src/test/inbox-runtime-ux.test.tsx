import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, renderHook, screen, within } from '@testing-library/react';

const toastMock = vi.hoisted(() =>
  Object.assign(
    vi.fn<(message: string, options?: unknown) => string>(() => 'toast-1'),
    {
      error: vi.fn<(message: string, options?: unknown) => string>(() => 'toast-err'),
      dismiss: vi.fn<(id?: string | number) => void>(),
    }
  )
);

vi.mock('sonner', () => ({ toast: toastMock }));

import {
  DEFAULT_UNDO_WINDOW_MS,
  useOptimisticUndo,
} from '@/features/mailbox/hooks/use-optimistic-undo';
import {
  KEYBOARD_SHORTCUT_GROUPS,
  KeyboardShortcutsDialog,
  useShortcutHelpHotkey,
} from '@/features/mailbox/components/keyboard-shortcuts-dialog';

interface ToastActionOptions {
  duration?: number;
  action?: {
    label: string;
    onClick: () => void;
  };
}

describe('useOptimisticUndo', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    toastMock.mockClear();
    toastMock.error.mockClear();
    toastMock.dismiss.mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('applies optimistic update immediately (0ms) and shows an Undo toast', () => {
    const { result } = renderHook(() => useOptimisticUndo());
    const optimisticApply = vi.fn();
    const rollback = vi.fn();
    const commit = vi.fn().mockResolvedValue('ok');

    result.current.execute({
      kind: 'archive',
      message: 'Conversation archived',
      optimisticApply,
      rollback,
      commit,
    });

    expect(optimisticApply).toHaveBeenCalledOnce();
    expect(commit).not.toHaveBeenCalled();
    expect(rollback).not.toHaveBeenCalled();
    expect(toastMock).toHaveBeenCalledWith(
      'Conversation archived',
      expect.objectContaining({
        duration: DEFAULT_UNDO_WINDOW_MS,
        action: expect.objectContaining({ label: 'Undo' }),
      })
    );
  });

  it('cancels the pending commit and runs rollback immediately when Undo is clicked before deadline', () => {
    const { result } = renderHook(() => useOptimisticUndo());
    const optimisticApply = vi.fn();
    const rollback = vi.fn();
    const commit = vi.fn().mockResolvedValue('ok');
    const onUndone = vi.fn();

    result.current.execute({
      kind: 'delete',
      message: 'Moved to trash',
      undoWindowMs: 4000,
      optimisticApply,
      rollback,
      commit,
      onUndone,
    });

    const options = toastMock.mock.calls[0]?.[1] as ToastActionOptions;
    expect(options.action?.label).toBe('Undo');

    vi.advanceTimersByTime(2000);
    expect(commit).not.toHaveBeenCalled();

    options.action?.onClick();
    expect(rollback).toHaveBeenCalledOnce();
    expect(onUndone).toHaveBeenCalledOnce();
    expect(toastMock.dismiss).toHaveBeenCalledWith('toast-1');

    // Advancing past the original deadline must not trigger commit.
    vi.advanceTimersByTime(5000);
    expect(commit).not.toHaveBeenCalled();

    // A duplicate click is a no-op.
    options.action?.onClick();
    expect(rollback).toHaveBeenCalledOnce();
  });

  it('commits asynchronously after the undo window expires and notifies onCommitted', async () => {
    const { result } = renderHook(() => useOptimisticUndo());
    const optimisticApply = vi.fn();
    const rollback = vi.fn();
    const commit = vi.fn().mockResolvedValue({ archived: true });
    const onCommitted = vi.fn();

    result.current.execute({
      kind: 'archive',
      message: 'Archived',
      undoWindowMs: 5000,
      optimisticApply,
      rollback,
      commit,
      onCommitted,
    });

    vi.advanceTimersByTime(4999);
    expect(commit).not.toHaveBeenCalled();

    await act(async () => {
      vi.advanceTimersByTime(1);
      await Promise.resolve();
    });

    expect(commit).toHaveBeenCalledOnce();
    expect(onCommitted).toHaveBeenCalledWith({ archived: true });
    expect(rollback).not.toHaveBeenCalled();
    expect(toastMock.error).not.toHaveBeenCalled();
  });

  it('rolls back automatically and shows an error toast when commit rejects', async () => {
    const { result } = renderHook(() => useOptimisticUndo());
    const optimisticApply = vi.fn();
    const rollback = vi.fn();
    const failure = new Error('Network unavailable');
    const commit = vi.fn().mockRejectedValue(failure);
    const onError = vi.fn();

    result.current.execute({
      kind: 'star',
      message: 'Conversation starred',
      errorMessage: 'Failed to star conversation',
      undoWindowMs: 3000,
      optimisticApply,
      rollback,
      commit,
      onError,
    });

    await act(async () => {
      vi.advanceTimersByTime(3000);
      await Promise.resolve();
    });

    expect(commit).toHaveBeenCalledOnce();
    expect(rollback).toHaveBeenCalledOnce();
    expect(toastMock.error).toHaveBeenCalledWith('Failed to star conversation');
    expect(onError).toHaveBeenCalledWith(failure);
  });

  it('supports immediateCommit while still rolling back on rejection', async () => {
    const { result } = renderHook(() => useOptimisticUndo());
    const optimisticApply = vi.fn();
    const rollback = vi.fn();
    const commit = vi.fn().mockRejectedValue(new Error('500'));

    await act(async () => {
      result.current.execute({
        kind: 'markRead',
        message: 'Marked as read',
        immediateCommit: true,
        optimisticApply,
        rollback,
        commit,
      });
      await Promise.resolve();
    });

    expect(optimisticApply).toHaveBeenCalledOnce();
    expect(commit).toHaveBeenCalledOnce();
    expect(rollback).toHaveBeenCalledOnce();
    expect(toastMock.error).toHaveBeenCalledOnce();
  });

  it('cancels pending timers cleanly on unmount by default', () => {
    const { result, unmount } = renderHook(() => useOptimisticUndo());
    const commit = vi.fn().mockResolvedValue('ok');

    result.current.execute({
      kind: 'send',
      message: 'Sending…',
      undoWindowMs: 5000,
      optimisticApply: vi.fn(),
      rollback: vi.fn(),
      commit,
    });

    unmount();
    vi.advanceTimersByTime(10_000);
    expect(commit).not.toHaveBeenCalled();
  });

  it('flushes pending commits on unmount when configured with onUnmount: "flush"', () => {
    const { result, unmount } = renderHook(() => useOptimisticUndo({ onUnmount: 'flush' }));
    const commit = vi.fn().mockResolvedValue('ok');

    result.current.execute({
      kind: 'archive',
      message: 'Archived',
      undoWindowMs: 5000,
      optimisticApply: vi.fn(),
      rollback: vi.fn(),
      commit,
    });

    unmount();
    expect(commit).toHaveBeenCalledOnce();
  });
});

function ShortcutHarness() {
  const [open, setOpen] = useState(false);
  useShortcutHelpHotkey(() => setOpen(true));

  return (
    <div>
      <input aria-label="Search mail" type="text" />
      <textarea aria-label="Quick reply" />
      <div aria-label="Rich editor" contentEditable suppressContentEditableWarning />
      <KeyboardShortcutsDialog open={open} onOpenChange={setOpen} />
    </div>
  );
}

describe('KeyboardShortcutsDialog & useShortcutHelpHotkey', () => {
  it('opens the dialog when pressing ? outside inputs and renders all shortcut groups cleanly', () => {
    render(<ShortcutHarness />);

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    fireEvent.keyDown(window, { key: '?', shiftKey: true });

    const dialog = screen.getByRole('dialog', { name: 'Keyboard shortcuts' });
    expect(dialog).toBeInTheDocument();

    for (const group of KEYBOARD_SHORTCUT_GROUPS) {
      expect(within(dialog).getByRole('heading', { name: group.title })).toBeInTheDocument();
      for (const item of group.items) {
        expect(within(dialog).getByText(item.label)).toBeInTheDocument();
        for (const key of item.keys) {
          expect(within(dialog).getByText(key)).toBeInTheDocument();
        }
      }
    }
  });

  it('does NOT open the dialog when pressing ? inside an <input>, <textarea>, or contentEditable element', () => {
    render(<ShortcutHarness />);

    const input = screen.getByRole('textbox', { name: 'Search mail' });
    input.focus();
    fireEvent.keyDown(input, { key: '?', shiftKey: true });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    const textarea = screen.getByRole('textbox', { name: 'Quick reply' });
    textarea.focus();
    fireEvent.keyDown(textarea, { key: '?', shiftKey: true });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    const editor = screen.getByLabelText('Rich editor');
    editor.focus();
    fireEvent.keyDown(editor, { key: '?', shiftKey: true });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});
