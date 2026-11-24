'use client';

import * as React from 'react';
import { Building2, User } from 'lucide-react';

import {
  useWorkspaceModeStore,
  type WorkspaceMode,
} from '@/features/mailbox/stores/workspace-mode';
import { cn } from '@/utils';

export interface WorkspaceModeToggleProps {
  /** Controlled mode value. When omitted, reads from `useWorkspaceModeStore`. */
  value?: WorkspaceMode;
  /** Controlled change handler. When omitted, updates `useWorkspaceModeStore`. */
  onChange?: (mode: WorkspaceMode) => void;
  /** Accessible name for the radio group container. */
  ariaLabel?: string;
  /** Optional localized labels for the two segments. */
  labels?: {
    personal?: string;
    work?: string;
  };
  /** Visual size variant for sidebar or header placement. */
  size?: 'sm' | 'default';
  className?: string;
}

const MODE_OPTIONS: ReadonlyArray<{
  value: WorkspaceMode;
  defaultLabel: string;
  Icon: typeof User;
}> = [
  { value: 'personal', defaultLabel: 'Personal', Icon: User },
  { value: 'work', defaultLabel: 'Work', Icon: Building2 },
];

export function WorkspaceModeToggle({
  value,
  onChange,
  ariaLabel = 'Workspace mode',
  labels,
  size = 'default',
  className,
}: WorkspaceModeToggleProps) {
  const storeMode = useWorkspaceModeStore(state => state.mode);
  const setStoreMode = useWorkspaceModeStore(state => state.setMode);
  const hydrate = useWorkspaceModeStore(state => state.hydrate);

  const isControlled = value !== undefined;
  const activeMode = isControlled ? value : storeMode;

  React.useEffect(() => {
    if (!isControlled) {
      hydrate();
    }
  }, [isControlled, hydrate]);

  const optionRefs = React.useRef<Array<HTMLButtonElement | null>>([]);

  const selectMode = React.useCallback(
    (nextMode: WorkspaceMode) => {
      if (nextMode === activeMode) {
        return;
      }
      if (onChange) {
        onChange(nextMode);
      }
      if (!isControlled) {
        setStoreMode(nextMode);
      }
    },
    [activeMode, isControlled, onChange, setStoreMode]
  );

  const handleKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>, index: number) => {
    let nextIndex: number | null = null;
    if (event.key === 'ArrowRight' || event.key === 'ArrowDown') {
      nextIndex = (index + 1) % MODE_OPTIONS.length;
    } else if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') {
      nextIndex = (index - 1 + MODE_OPTIONS.length) % MODE_OPTIONS.length;
    } else if (event.key === 'Home') {
      nextIndex = 0;
    } else if (event.key === 'End') {
      nextIndex = MODE_OPTIONS.length - 1;
    }

    if (nextIndex !== null) {
      event.preventDefault();
      const targetOption = MODE_OPTIONS[nextIndex];
      if (targetOption) {
        selectMode(targetOption.value);
        optionRefs.current[nextIndex]?.focus();
      }
    }
  };

  return (
    <div
      role="radiogroup"
      aria-label={ariaLabel}
      data-slot="workspace-mode-toggle"
      data-mode={activeMode}
      className={cn(
        'inline-flex items-center rounded-lg border border-border/80 bg-muted/70 p-0.5 dark:border-border/60 dark:bg-muted/40',
        className
      )}
    >
      {MODE_OPTIONS.map(({ value: optionValue, defaultLabel, Icon }, index) => {
        const isSelected = activeMode === optionValue;
        const label =
          optionValue === 'personal'
            ? (labels?.personal ?? defaultLabel)
            : (labels?.work ?? defaultLabel);

        return (
          <button
            key={optionValue}
            ref={node => {
              optionRefs.current[index] = node;
            }}
            type="button"
            role="radio"
            aria-checked={isSelected}
            tabIndex={isSelected ? 0 : -1}
            data-state={isSelected ? 'checked' : 'unchecked'}
            data-mode-option={optionValue}
            onClick={() => selectMode(optionValue)}
            onKeyDown={event => handleKeyDown(event, index)}
            className={cn(
              'inline-flex flex-row items-center justify-center gap-1.5 whitespace-nowrap rounded-md font-medium transition-all select-none focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1',
              size === 'sm' ? 'h-7 px-2.5 text-xs' : 'h-8 px-3 text-sm',
              isSelected
                ? 'bg-background text-foreground shadow-xs ring-1 ring-border/50 dark:bg-card dark:text-foreground dark:ring-border/70'
                : 'text-muted-foreground hover:text-foreground dark:text-muted-foreground dark:hover:text-foreground'
            )}
          >
            <Icon className={cn('shrink-0', size === 'sm' ? 'size-3.5' : 'size-4')} aria-hidden="true" />
            <span>{label}</span>
          </button>
        );
      })}
    </div>
  );
}
