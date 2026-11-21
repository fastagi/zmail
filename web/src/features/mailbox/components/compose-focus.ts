'use client';

import { useEffect, useRef } from 'react';

/** The marker every control that opens a new message carries: `data-compose-trigger`. */
const COMPOSE_TRIGGER_ATTRIBUTE = 'data-compose-trigger';

/**
 * A selector that finds `target` again after the view holding it is rendered anew: the Compose
 * button, a mail row, or an element with an id. Null for anything else.
 */
function focusSelector(target: EventTarget | null): string | null {
  if (!(target instanceof HTMLElement)) return null;
  if (target.closest(`[${COMPOSE_TRIGGER_ATTRIBUTE}]`)) return `[${COMPOSE_TRIGGER_ATTRIBUTE}]`;
  const row = target.closest<HTMLElement>('[data-row-id]');
  if (row) return `[data-row-id="${CSS.escape(row.dataset.rowId ?? '')}"]`;
  return target.id ? `#${CSS.escape(target.id)}` : null;
}

/**
 * The page's composer takes the place of the workspace, so the control that opened it is gone
 * while it is open. This remembers what last had the focus before it opened and, once it closes
 * and the workspace is back, gives the focus back to that control as it is rendered now.
 */
export function useComposeFocusReturn(open: boolean): void {
  const target = useRef<string | null>(null);
  const wasOpen = useRef(false);

  useEffect(() => {
    if (open) return;
    const remember = (event: FocusEvent) => {
      target.current = focusSelector(event.target);
    };
    document.addEventListener('focusin', remember);
    return () => document.removeEventListener('focusin', remember);
  }, [open]);

  useEffect(() => {
    if (open) {
      wasOpen.current = true;
      return;
    }
    if (!wasOpen.current) return;
    wasOpen.current = false;
    if (target.current) document.querySelector<HTMLElement>(target.current)?.focus();
  }, [open]);
}
