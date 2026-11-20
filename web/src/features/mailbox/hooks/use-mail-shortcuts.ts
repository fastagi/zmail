'use client';

import { useEffect, useEffectEvent } from 'react';

/** The actions of the unified inbox keyboard (§11.7). */
export type MailShortcut =
  | 'next'
  | 'previous'
  | 'open'
  | 'archive'
  | 'trash'
  | 'star'
  | 'reply'
  | 'replyAll'
  | 'forward'
  | 'triage'
  | 'compose'
  | 'search'
  | 'help'
  | 'palette'
  | 'escape';

/** Keys pressed without a modifier (Shift aside, which `#` and `?` need). */
const singleKeys: Readonly<Record<string, MailShortcut>> = {
  j: 'next',
  ArrowDown: 'next',
  k: 'previous',
  ArrowUp: 'previous',
  Enter: 'open',
  e: 'archive',
  '#': 'trash',
  s: 'star',
  r: 'reply',
  a: 'replyAll',
  f: 'forward',
  // Shift+F: `f` is Forward, so the AI triage menu takes the capital.
  F: 'triage',
  c: 'compose',
  '/': 'search',
  '?': 'help',
};

/** Moving may repeat while the key is held; an action runs once per press. */
const repeatable = new Set<MailShortcut>(['next', 'previous']);

/** Whether the key went to a field or editable text, where single keys are text. */
export function isTyping(target: EventTarget | null): boolean {
  return (
    target instanceof HTMLElement &&
    (target.isContentEditable || target.closest('input, textarea, select') !== null)
  );
}

/** Whether a dialog is open, which keeps its keys (`Esc` included) for itself. */
export function modalOpen(): boolean {
  return (
    document.querySelector('[role="dialog"][data-state="open"], [role="alertdialog"]') !== null
  );
}

function shortcutOf(event: KeyboardEvent): MailShortcut | null {
  if (
    (event.metaKey || event.ctrlKey) &&
    !event.altKey &&
    !event.shiftKey &&
    event.key.toLowerCase() === 'k'
  ) {
    return 'palette';
  }
  if (event.metaKey || event.ctrlKey || event.altKey) return null;
  if (event.key === 'Escape') return 'escape';
  if (event.isComposing || isTyping(event.target) || modalOpen()) return null;
  // Enter on a control belongs to that control.
  if (event.key === 'Enter' && event.target !== document.body) return null;
  return singleKeys[event.key] ?? null;
}

/**
 * Binds the unified inbox keys on the page (D29): `j` `k` (and the arrow keys) `Enter` `e` `#`
 * `s` `r` `a` `f` `c` `/` `?`, `⌘K`/`Ctrl+K`, and `Esc`. Single keys wait while focus is in a
 * field, during IME composition, and while a dialog is open; `⌘K` and `Esc` work anywhere, except
 * that an open dialog keeps `Esc` for itself. Other modifier combinations stay the browser's, and
 * a key some control already handled is left alone. Only bound keys are taken from the browser.
 */
export function useMailShortcuts(
  bindings: Partial<Record<MailShortcut, () => void>>,
  { enabled }: { enabled: boolean }
) {
  const onKeyDown = useEffectEvent((event: KeyboardEvent) => {
    if (event.defaultPrevented) return;
    const shortcut = shortcutOf(event);
    if (shortcut === null || (shortcut === 'escape' && modalOpen())) return;
    const action = bindings[shortcut];
    if (!action) return;
    event.preventDefault();
    if (event.repeat && !repeatable.has(shortcut)) return;
    action();
  });

  useEffect(() => {
    if (!enabled) return;
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [enabled]);
}
