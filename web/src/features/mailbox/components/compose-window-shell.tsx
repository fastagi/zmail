'use client';

import type { ReactNode, RefObject } from 'react';
import { ChevronUp, Maximize2, Minimize2, Minus, X } from 'lucide-react';

import { useT } from '@/i18n';
import { cn } from '@/utils';

export type ComposeWindowMode = 'docked' | 'minimized' | 'expanded';

interface ComposeWindowShellProps {
  dialogRef: RefObject<HTMLDivElement | null>;
  windowMode: ComposeWindowMode;
  onWindowModeChange: (mode: ComposeWindowMode) => void;
  titleId: string;
  descriptionId: string;
  accessibleTitle: string;
  displayTitle: string;
  closeDisabled: boolean;
  onDismiss: () => void;
  children: ReactNode;
}

/**
 * Gmail-style bottom-right docked floating composer shell with Minimize, Expand-to-Modal, and
 * Close controls. Keeps `children` mounted across every window mode so draft text, recipient
 * chips, and attached files are preserved without remounting.
 */
export function ComposeWindowShell({
  dialogRef,
  windowMode,
  onWindowModeChange,
  titleId,
  descriptionId,
  accessibleTitle,
  displayTitle,
  closeDisabled,
  onDismiss,
  children,
}: ComposeWindowShellProps) {
  const t = useT('mailbox');
  const isMinimized = windowMode === 'minimized';
  const isExpanded = windowMode === 'expanded';
  const headerButtonClass =
    'inline-flex size-7 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-background/70 hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50';

  return (
    <>
      {isExpanded ? (
        <div
          aria-hidden="true"
          data-compose-backdrop="true"
          className="fixed inset-0 z-40 bg-black/40 backdrop-blur-[1px]"
          onClick={() => onWindowModeChange('docked')}
        />
      ) : null}
      <div
        ref={dialogRef}
        role="dialog"
        aria-label={accessibleTitle}
        aria-describedby={descriptionId}
        data-state={isMinimized ? 'minimized' : 'open'}
        data-compose-mode={windowMode}
        className={cn(
          'fixed z-50 flex flex-col overflow-hidden border border-border/80 bg-background shadow-2xl',
          windowMode === 'docked' &&
            'right-5 bottom-0 max-h-[82vh] w-[560px] max-w-[calc(100vw-2rem)] rounded-t-xl',
          windowMode === 'minimized' &&
            'right-5 bottom-0 h-11 w-72 max-w-[calc(100vw-2rem)] rounded-t-xl',
          windowMode === 'expanded' &&
            'top-1/2 left-1/2 h-[80vh] max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-3xl -translate-x-1/2 -translate-y-1/2 rounded-xl'
        )}
      >
        <header
          data-compose-header="true"
          onClick={isMinimized ? () => onWindowModeChange('docked') : undefined}
          className={cn(
            'flex h-11 shrink-0 items-center justify-between gap-2 border-b border-border/70 bg-muted/80 px-3.5 text-foreground dark:bg-muted/60',
            isMinimized && 'cursor-pointer border-b-0 hover:bg-muted'
          )}
        >
          <h2 id={titleId} aria-label={accessibleTitle} className="truncate text-sm font-semibold">
            {displayTitle}
          </h2>
          <div className="flex shrink-0 items-center gap-0.5">
            {isMinimized ? (
              <button
                type="button"
                aria-label={t('compose.restore')}
                onClick={event => {
                  event.stopPropagation();
                  onWindowModeChange('docked');
                }}
                className={headerButtonClass}
              >
                <ChevronUp className="size-4" aria-hidden="true" />
              </button>
            ) : (
              <>
                <button
                  type="button"
                  aria-label={t('compose.minimize')}
                  onClick={() => onWindowModeChange('minimized')}
                  className={headerButtonClass}
                >
                  <Minus className="size-4" aria-hidden="true" />
                </button>
                <button
                  type="button"
                  aria-label={isExpanded ? t('compose.dock') : t('compose.expand')}
                  onClick={() => onWindowModeChange(isExpanded ? 'docked' : 'expanded')}
                  className={headerButtonClass}
                >
                  {isExpanded ? (
                    <Minimize2 className="size-3.5" aria-hidden="true" />
                  ) : (
                    <Maximize2 className="size-3.5" aria-hidden="true" />
                  )}
                </button>
              </>
            )}
            <button
              type="button"
              aria-label={t('actions.close')}
              disabled={closeDisabled}
              onClick={event => {
                event.stopPropagation();
                onDismiss();
              }}
              className={headerButtonClass}
            >
              <X className="size-4" aria-hidden="true" />
            </button>
          </div>
        </header>
        {children}
      </div>
    </>
  );
}
