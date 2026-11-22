'use client';

import { useEffect, useRef, useState } from 'react';
import { Check, Copy } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useT } from '@/i18n';

/**
 * Copies the one-time invitation link a create or resend call returned. The link is a bearer
 * secret: it lives only in the caller's component state and is shown in a selectable field only
 * when the Clipboard API is missing or refuses, so the manager can still copy it by hand.
 */
export function InvitationLinkPanel({ email, url }: { email: string; url: string }) {
  const t = useT('organization');
  const [copied, setCopied] = useState(false);
  const [manual, setManual] = useState(false);
  const field = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 2_000);
    return () => window.clearTimeout(timer);
  }, [copied]);

  useEffect(() => {
    if (manual) field.current?.select();
  }, [manual]);

  const copy = async () => {
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard API unavailable');
      await navigator.clipboard.writeText(url);
      setCopied(true);
    } catch {
      setCopied(false);
      setManual(true);
    }
  };

  return (
    <div className="space-y-3">
      <Button
        type="button"
        variant="outline"
        icon={copied ? <Check className="size-4" /> : <Copy className="size-4" />}
        onClick={() => void copy()}
      >
        {copied ? t('invitationLink.copied') : t('invitationLink.copy')}
      </Button>
      <p role="status" className="sr-only">
        {copied ? t('invitationLink.copiedAnnouncement') : ''}
      </p>
      {manual ? (
        <div className="space-y-2">
          <p className="text-sm text-error">{t('invitationLink.copyFailed')}</p>
          <Input
            ref={field}
            readOnly
            value={url}
            aria-label={t('invitationLink.field', { email })}
            spellCheck={false}
            autoComplete="off"
            className="font-mono text-xs"
            onFocus={(event) => event.currentTarget.select()}
          />
        </div>
      ) : null}
      <p className="text-xs text-muted-foreground">{t('invitationLink.acceptHint', { email })}</p>
    </div>
  );
}
