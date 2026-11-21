'use client';

import { useLayoutEffect, useRef, type RefObject } from 'react';

/** Set on the content of each dialog in a chain that shares one return target. */
const RETURN_FOCUS_CHAIN_ATTRIBUTE = 'data-return-focus-chain';

/**
 * Focus for a dialog opened by state rather than by a `DialogTrigger`: Radix returns focus only to
 * its own trigger, so without one a closed dialog leaves focus on the page body. This remembers
 * the element that had focus when the dialog opened and focuses it again on every close path
 * (Escape, the close button, the backdrop, or the page closing it).
 *
 * Dialogs that open one another share `returnFocusRef`: a dialog opened from inside another one
 * of the chain keeps the target the first one remembered, and a dialog that closes while focus
 * already sits in the next one leaves focus there.
 *
 * Returns the `onCloseAutoFocus` handler for `DialogContent`.
 */
export function useDialogReturnFocus(
  open: boolean,
  returnFocusRef?: RefObject<HTMLElement | null>
): (event: Event) => void {
  const ownRef = useRef<HTMLElement | null>(null);
  const target = returnFocusRef ?? ownRef;

  // A layout effect runs before Radix moves focus into the dialog in its passive effect.
  useLayoutEffect(() => {
    if (!open) return;
    const active = document.activeElement;
    if (!(active instanceof HTMLElement) || active === document.body) return;
    if (active.closest(`[${RETURN_FOCUS_CHAIN_ATTRIBUTE}]`)) return;
    target.current = active;
  }, [open, target]);

  return event => {
    event.preventDefault();
    const element = target.current;
    if (!element?.isConnected) return;
    // Another dialog of the chain opened as this one closed and already holds focus.
    if (document.activeElement?.closest(`[${RETURN_FOCUS_CHAIN_ATTRIBUTE}]`)) return;
    element.focus();
  };
}
