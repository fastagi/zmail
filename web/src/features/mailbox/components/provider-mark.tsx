import { Mail } from 'lucide-react';

import { providerName } from '@/features/mailbox/utils/external-mail';
import { cn } from '@/utils';

/** A neutral tile with the provider's initial; brand artwork stays out of the scaffold. */
export function ProviderMark({ id, className }: { id: string; className?: string }) {
  const initial = providerName(id).charAt(0);
  return (
    <span
      aria-hidden="true"
      className={cn(
        'grid size-6 shrink-0 place-items-center rounded-md bg-row-selected text-[11px] font-semibold text-text-muted',
        className
      )}
    >
      {initial || <Mail className="size-3.5" />}
    </span>
  );
}
