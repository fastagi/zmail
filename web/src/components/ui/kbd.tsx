/**
 * @component Kbd
 * @category UI
 * @status Stable
 * @description A key cap that names a keyboard shortcut beside the control it triggers.
 * @usage Put it after a label (`Reply <Kbd>R</Kbd>`). Inside a control whose name is its label,
 * mark the cap `aria-hidden` so the key is not read as part of the name.
 * @example
 * <Kbd>⌘K</Kbd>
 */
import * as React from 'react';

import { cn } from '@/utils';

function Kbd({ className, ...props }: React.ComponentProps<'kbd'>) {
  return (
    <kbd
      data-slot="kbd"
      className={cn(
        'inline-flex h-[18px] min-w-[18px] shrink-0 items-center justify-center rounded-[5px] border border-b-2 border-border-main bg-bg-surface px-[5px] font-sans text-[10.5px] leading-none font-normal text-text-muted',
        className
      )}
      {...props}
    />
  );
}

export { Kbd };
