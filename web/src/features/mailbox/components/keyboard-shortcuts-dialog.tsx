'use client';

import { useEffect, useEffectEvent, useId } from 'react';
import { Keyboard } from 'lucide-react';

import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Kbd } from '@/components/ui/kbd';
import { cn } from '@/utils';

export interface ShortcutItem {
  /** Stable identifier for the shortcut row. */
  id: string;
  /** Human-readable action description. */
  label: string;
  /** Key cap(s) rendered in order (e.g., `['J']` or `['⌘K']`). */
  keys: readonly string[];
}

export interface ShortcutGroup {
  /** Stable group identifier. */
  id: string;
  /** Group heading shown above the shortcut list. */
  title: string;
  /** Ordered shortcuts belonging to this group. */
  items: readonly ShortcutItem[];
}

/** Default Superhuman-style keyboard shortcut groups for the inbox cheat-sheet. */
export const KEYBOARD_SHORTCUT_GROUPS: readonly ShortcutGroup[] = [
  {
    id: 'navigation',
    title: 'Navigation',
    items: [
      { id: 'next', label: 'Next conversation', keys: ['J'] },
      { id: 'previous', label: 'Previous conversation', keys: ['K'] },
      { id: 'open', label: 'Open conversation', keys: ['Enter'] },
      { id: 'escape', label: 'Close / Back', keys: ['Esc'] },
    ],
  },
  {
    id: 'actions',
    title: 'Actions',
    items: [
      { id: 'compose', label: 'Compose', keys: ['C'] },
      { id: 'reply', label: 'Reply', keys: ['R'] },
      { id: 'archive', label: 'Archive', keys: ['E'] },
      { id: 'star', label: 'Star', keys: ['S'] },
      { id: 'delete', label: 'Delete', keys: ['#'] },
    ],
  },
  {
    id: 'search-and-mode',
    title: 'Search & Mode',
    items: [
      { id: 'search', label: 'Search', keys: ['/'] },
      { id: 'command-palette', label: 'Command Palette', keys: ['⌘K'] },
      { id: 'shortcuts', label: 'Shortcuts', keys: ['?'] },
    ],
  },
] as const;

/**
 * Returns `true` when `target` is inside an editable form control or `contentEditable`
 * region where single-key shortcuts like `?` must not steal user input.
 */
export function isEditableEventTarget(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return false;
  if (target instanceof HTMLElement && target.isContentEditable) return true;
  if (
    target.closest(
      'input, textarea, select, [contenteditable=""], [contenteditable="true"], [contenteditable="plaintext-only"]'
    ) !== null
  ) {
    return true;
  }
  return false;
}

export interface UseShortcutHelpHotkeyOptions {
  /** Whether the global `?` listener is active. Defaults to `true`. */
  enabled?: boolean;
}

/**
 * Listens for `?` (`Shift+/`) on `window` and invokes `onOpen` when focus is not inside
 * an `<input>`, `<textarea>`, `<select>`, or `contentEditable` element.
 */
export function useShortcutHelpHotkey(
  onOpen: () => void,
  options: UseShortcutHelpHotkeyOptions = {}
) {
  const { enabled = true } = options;

  const handleKeyDown = useEffectEvent((event: KeyboardEvent) => {
    if (event.defaultPrevented || event.isComposing || event.repeat) return;
    if (event.metaKey || event.ctrlKey || event.altKey) return;

    const isQuestionMark = event.key === '?' || (event.key === '/' && event.shiftKey);
    if (!isQuestionMark) return;

    if (isEditableEventTarget(event.target) || isEditableEventTarget(document.activeElement)) {
      return;
    }

    event.preventDefault();
    onOpen();
  });

  useEffect(() => {
    if (!enabled) return;
    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [enabled]);
}

export interface KeyboardShortcutsDialogProps {
  /** Controls whether the cheat-sheet dialog is open. */
  open: boolean;
  /** Called when the dialog requests an open-state change (Esc, backdrop click, close button). */
  onOpenChange: (open: boolean) => void;
  /** Optional callback to customize focus restoration on close. */
  onCloseAutoFocus?: (event: Event) => void;
  /** Shortcut groups to display. Defaults to `KEYBOARD_SHORTCUT_GROUPS`. */
  groups?: readonly ShortcutGroup[];
  /** Dialog title. Defaults to `"Keyboard shortcuts"`. */
  title?: string;
  /** Dialog subtitle. */
  description?: string;
  /** Accessible label for the close button. Defaults to `"Close"`. */
  closeLabel?: string;
  /** Additional classes applied to the dialog content container. */
  className?: string;
}

/**
 * Crisp, accessible Keyboard Shortcuts Cheat-Sheet dialog organized into logical groups
 * (Navigation, Actions, Search & Mode).
 */
export function KeyboardShortcutsDialog({
  open,
  onOpenChange,
  onCloseAutoFocus,
  groups = KEYBOARD_SHORTCUT_GROUPS,
  title = 'Keyboard shortcuts',
  description = 'Navigate and triage your inbox at keyboard speed.',
  closeLabel = 'Close',
  className,
}: KeyboardShortcutsDialogProps) {
  const baseId = useId();

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        size="default"
        closeLabel={closeLabel}
        onCloseAutoFocus={onCloseAutoFocus}
        className={cn('gap-5', className)}
      >
        <DialogHeader>
          <div className="flex items-center gap-2.5">
            <span
              aria-hidden="true"
              className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-border/60 bg-muted/50 text-foreground"
            >
              <Keyboard className="size-4" />
            </span>
            <div className="flex flex-col gap-0.5 text-left">
              <DialogTitle>{title}</DialogTitle>
              <DialogDescription>{description}</DialogDescription>
            </div>
          </div>
        </DialogHeader>

        <DialogBody>
          <div className="flex flex-col gap-5 pb-1">
            {groups.map(group => {
              const headingId = `${baseId}-${group.id}`;
              return (
                <section key={group.id} aria-labelledby={headingId} className="flex flex-col gap-1.5">
                  <h3
                    id={headingId}
                    className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground"
                  >
                    {group.title}
                  </h3>
                  <dl className="divide-y divide-border/50 rounded-lg border border-border/60 bg-muted/20 px-3 text-[13px]">
                    {group.items.map(item => (
                      <div
                        key={item.id}
                        className="flex items-center justify-between gap-4 py-2"
                      >
                        <dt className="text-foreground/90">{item.label}</dt>
                        <dd className="flex shrink-0 items-center gap-1.5">
                          {item.keys.map(key => (
                            <Kbd key={key}>{key}</Kbd>
                          ))}
                        </dd>
                      </div>
                    ))}
                  </dl>
                </section>
              );
            })}
          </div>
        </DialogBody>
      </DialogContent>
    </Dialog>
  );
}
