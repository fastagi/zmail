'use client';

import { useEffect, useRef, useState } from 'react';
import { Check, Copy } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { cn } from '@/utils';

export function CopyAddressButton({
  address,
  label,
  copiedLabel,
  failedLabel,
  size = 'sm',
  className,
}: {
  address: string;
  label: string;
  copiedLabel: string;
  failedLabel: string;
  size?: 'xs' | 'sm';
  className?: string;
}) {
  const [copied, setCopied] = useState(false);
  const timerRef = useRef<number | null>(null);

  useEffect(() => {
    return () => {
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
    };
  }, []);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(address);
      setCopied(true);
      toast.success(copiedLabel);
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
      timerRef.current = window.setTimeout(() => setCopied(false), 1_600);
    } catch {
      setCopied(false);
      toast.error(failedLabel);
    }
  };

  return (
    <Button
      type="button"
      isIcon
      size={size}
      variant="ghost"
      aria-label={copied ? copiedLabel : label}
      title={copied ? copiedLabel : label}
      className={cn(
        'shrink-0 text-muted-foreground hover:text-foreground',
        copied && 'text-emerald-600 hover:text-emerald-600 dark:text-emerald-400',
        className
      )}
      onClick={event => {
        event.stopPropagation();
        void copy();
      }}
    >
      {copied ? (
        <Check className="size-3.5" aria-hidden="true" />
      ) : (
        <Copy className="size-3.5" aria-hidden="true" />
      )}
    </Button>
  );
}
