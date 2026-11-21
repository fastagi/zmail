import * as React from 'react';
import * as DialogPrimitive from '@radix-ui/react-dialog';

import { cn } from '@/lib/cn';

const Dialog = DialogPrimitive.Root;
const DialogClose = DialogPrimitive.Close;

const DialogContent = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content>
>(({ className, children, onOpenAutoFocus, onCloseAutoFocus, ...props }, ref) => {
  // Admin dialogs are opened by state from row buttons or shortcuts, not by a DialogTrigger, and
  // Radix returns focus only to a trigger. Remember what had focus when the dialog opened and
  // return focus there on close, so keyboard users keep their place (#94).
  const returnFocusRef = React.useRef<HTMLElement | null>(null);

  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/60 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0" />
      <DialogPrimitive.Content
        ref={ref}
        className={cn(
          'fixed left-1/2 top-1/2 z-50 grid w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 gap-4 rounded-lg border bg-background p-6 shadow-lg',
          className,
        )}
        onOpenAutoFocus={(event) => {
          const active = document.activeElement;
          returnFocusRef.current =
            active instanceof HTMLElement && active !== document.body ? active : null;
          onOpenAutoFocus?.(event);
        }}
        onCloseAutoFocus={(event) => {
          onCloseAutoFocus?.(event);
          const target = returnFocusRef.current;
          returnFocusRef.current = null;
          if (!event.defaultPrevented && target?.isConnected) {
            event.preventDefault();
            target.focus();
          }
        }}
        {...props}
      >
        {children}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  );
});
DialogContent.displayName = DialogPrimitive.Content.displayName;

const DialogTitle = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Title
    ref={ref}
    className={cn('text-base font-semibold leading-none', className)}
    {...props}
  />
));
DialogTitle.displayName = DialogPrimitive.Title.displayName;

const DialogDescription = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Description
    ref={ref}
    className={cn('text-sm text-muted-foreground', className)}
    {...props}
  />
));
DialogDescription.displayName = DialogPrimitive.Description.displayName;

function DialogFooter({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn('flex flex-col-reverse gap-2 sm:flex-row sm:justify-end', className)}
      {...props}
    />
  );
}

export { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogTitle };
