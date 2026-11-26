'use client';

import {
  forwardRef,
  useRef,
  useState,
  type ChangeEvent,
  type ClipboardEvent,
  type KeyboardEvent,
  type ReactNode,
} from 'react';
import { X } from 'lucide-react';

import { FormControlError, useFormControlA11y } from '@/components/ui/form-control';
import { normalizeRecipient } from '@/features/mailbox/schemas';
import { cn } from '@/utils';

export interface RecipientChipsInputProps {
  id: string;
  name?: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  disabled?: boolean;
  ariaLabel?: string;
  ariaDescribedBy?: string;
  errorText?: ReactNode;
  className?: string;
  removeLabel: (address: string) => string;
  invalidChipLabel?: (address: string) => string;
}

const DELIMITER_RE = /[,;\r\n]+/u;
const HAS_DELIMITER_RE = /[,;\r\n]/u;

function splitTokens(raw: string): string[] {
  return raw
    .split(DELIMITER_RE)
    .map(part => part.trim())
    .filter(Boolean);
}

function appendUniqueChips(existing: readonly string[], incoming: readonly string[]): string[] {
  const seen = new Set(existing.map(item => item.toLowerCase()));
  const next = [...existing];
  for (const token of incoming) {
    const key = token.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    next.push(token);
  }
  return next;
}

function joinRecipients(chips: readonly string[], draftText: string): string {
  if (chips.length === 0) return draftText;
  const trimmed = draftText.trim();
  if (trimmed === '') return chips.join(', ');
  return [...chips, draftText].join(', ');
}

/**
 * Gmail-style recipient field that commits addresses into removable pill chips on Enter, comma,
 * semicolon, Tab, blur, or multi-address paste, while keeping the full comma-joined value synced
 * via `onChange` so typing an address and submitting immediately works without pressing Enter.
 */
export const RecipientChipsInput = forwardRef<HTMLTextAreaElement, RecipientChipsInputProps>(
  function RecipientChipsInput(
    {
      id,
      name,
      value,
      onChange,
      placeholder,
      disabled = false,
      ariaLabel,
      ariaDescribedBy,
      errorText,
      className,
      removeLabel,
      invalidChipLabel,
    },
    forwardedRef
  ) {
    const innerRef = useRef<HTMLTextAreaElement | null>(null);
    const [chips, setChips] = useState<string[]>([]);
    const [draftText, setDraftText] = useState<string>(value);
    const [syncedValue, setSyncedValue] = useState<string>(value);

    // Sync external value resets (e.g. switching draft or clearing from parent).
    if (value !== syncedValue) {
      setSyncedValue(value);
      setChips([]);
      setDraftText(value);
    }

    const controlA11y = useFormControlA11y({
      id,
      errorText,
      ariaDescribedBy,
    });

    const assignRef = (node: HTMLTextAreaElement | null) => {
      innerRef.current = node;
      if (typeof forwardedRef === 'function') forwardedRef(node);
      else if (forwardedRef) forwardedRef.current = node;
    };

    const emit = (nextChips: string[], nextDraft: string) => {
      const combined = joinRecipients(nextChips, nextDraft);
      setChips(nextChips);
      setDraftText(nextDraft);
      setSyncedValue(combined);
      onChange(combined);
    };

    const commitTokens = (raw: string) => {
      const tokens = splitTokens(raw);
      if (tokens.length === 0) {
        if (draftText !== '') emit(chips, '');
        return;
      }
      emit(appendUniqueChips(chips, tokens), '');
    };

    const handleChange = (event: ChangeEvent<HTMLTextAreaElement>) => {
      const next = event.target.value;
      // Soft keyboards and programmatic trailing-delimiter edits commit immediately.
      if ((next.endsWith(',') || next.endsWith(';')) && splitTokens(next).length > 0) {
        commitTokens(next);
        return;
      }
      emit(chips, next);
    };

    const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
      if (event.nativeEvent.isComposing) return;

      // Let Cmd/Ctrl+Enter bubble up to the compose form's send shortcut.
      if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
        return;
      }

      if (event.key === 'Enter' || event.key === ',' || event.key === ';') {
        event.preventDefault();
        if (draftText.trim() !== '') commitTokens(draftText);
        return;
      }

      if (event.key === 'Tab' && draftText.trim() !== '') {
        commitTokens(draftText);
        return;
      }

      if (event.key === 'Backspace' && draftText === '' && chips.length > 0) {
        event.preventDefault();
        emit(chips.slice(0, -1), '');
      }
    };

    const handlePaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
      const pasted = event.clipboardData.getData('text');
      if (!pasted) return;
      if (HAS_DELIMITER_RE.test(pasted) || normalizeRecipient(pasted) !== null) {
        event.preventDefault();
        const combined = draftText.trim() ? `${draftText}, ${pasted}` : pasted;
        commitTokens(combined);
      }
    };

    const handleBlur = () => {
      if (draftText.trim() !== '') {
        commitTokens(draftText);
      }
    };

    const removeChip = (index: number) => {
      emit(
        chips.filter((_, position) => position !== index),
        draftText
      );
      innerRef.current?.focus();
    };

    return (
      <div className="flex w-full flex-col gap-0.5">
        <div
          data-slot="recipient-chips-input"
          onClick={event => {
            if (disabled) return;
            if ((event.target as HTMLElement).closest('button')) return;
            innerRef.current?.focus();
          }}
          className={cn(
            'flex min-h-9 w-full flex-wrap items-center gap-1.5 rounded-lg border border-border bg-background px-2.5 py-1.5 text-sm shadow-xs transition-all hover:border-border-strong focus-within:border-primary focus-within:ring-1 focus-within:ring-primary/20 dark:bg-input/10',
            controlA11y.isInvalid &&
              'border-destructive focus-within:border-destructive focus-within:ring-destructive/20',
            disabled && 'cursor-not-allowed opacity-50',
            className
          )}
        >
          {chips.map((address, index) => {
            const invalid = normalizeRecipient(address) === null;
            return (
              <span
                key={`${address.toLowerCase()}-${index}`}
                data-recipient-chip="true"
                data-invalid={invalid ? 'true' : undefined}
                title={invalid ? invalidChipLabel?.(address) : address}
                className={cn(
                  'inline-flex max-w-full items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-medium',
                  invalid
                    ? 'border-destructive/50 bg-destructive/10 text-destructive'
                    : 'border-border/60 bg-secondary text-secondary-foreground'
                )}
              >
                <span className="truncate">{address}</span>
                <button
                  type="button"
                  disabled={disabled}
                  aria-label={removeLabel(address)}
                  onClick={() => removeChip(index)}
                  className="inline-flex size-3.5 shrink-0 cursor-pointer items-center justify-center rounded-full opacity-70 transition-opacity hover:bg-foreground/10 hover:opacity-100 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none"
                >
                  <X className="size-3" aria-hidden="true" />
                </button>
              </span>
            );
          })}
          <textarea
            ref={assignRef}
            id={id}
            name={name}
            rows={1}
            value={draftText}
            spellCheck={false}
            disabled={disabled}
            placeholder={chips.length === 0 ? placeholder : undefined}
            aria-label={ariaLabel}
            aria-describedby={controlA11y.ariaDescribedBy}
            aria-invalid={controlA11y.ariaInvalid}
            onChange={handleChange}
            onKeyDown={handleKeyDown}
            onPaste={handlePaste}
            onBlur={handleBlur}
            className="h-6 min-w-[140px] flex-1 resize-none overflow-hidden bg-transparent py-0.5 text-sm leading-5 text-foreground outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed"
          />
        </div>
        {errorText ? (
          <FormControlError id={controlA11y.errorId}>{errorText}</FormControlError>
        ) : null}
      </div>
    );
  }
);
