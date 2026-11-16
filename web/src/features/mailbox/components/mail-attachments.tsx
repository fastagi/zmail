'use client';

import { useState } from 'react';
import { Download, Paperclip } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type { MailAttachment, MailMessage } from '@/features/mailbox/types';
import { useT } from '@/i18n';

/** A short size such as `812 B`, `14.2 KB`, or `3.1 MB`. */
export function formatBytes(size: number): string {
  if (size < 1_024) return `${size} B`;
  if (size < 1_024 * 1_024) return `${(size / 1_024).toFixed(1)} KB`;
  return `${(size / (1_024 * 1_024)).toFixed(1)} MB`;
}

/** Reads a file as standard base64 without building one huge intermediate string. */
export async function fileToBase64(file: Blob): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  let binary = '';
  const chunk = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunk) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunk));
  }
  return btoa(binary);
}

/** A paperclip for list rows; the count is announced to screen readers. */
export function AttachmentIndicator({ count }: { count: number }) {
  const t = useT('mailbox');
  if (count < 1) return null;
  const label = t('list.attachments', { count });
  return (
    <Paperclip
      className="inline size-3.5 shrink-0 text-muted-foreground"
      role="img"
      aria-label={label}
    >
      <title>{label}</title>
    </Paperclip>
  );
}

/**
 * The files of a message with a download button each. Files are only ever saved, never opened in
 * the page: the API serves them as opaque downloads.
 */
export function MailAttachmentList({ message }: { message: MailMessage }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const [pending, setPending] = useState<number | null>(null);
  if (message.attachments.length === 0) return null;

  const download = async (attachment: MailAttachment) => {
    setPending(attachment.id);
    try {
      await mailboxService.downloadAttachment(
        organizationId,
        message.mailbox_id,
        message.id,
        attachment
      );
    } catch {
      toast.error(t('message.downloadFailed'));
    } finally {
      setPending(null);
    }
  };

  return (
    <section aria-label={t('message.attachments')} className="space-y-2">
      <h3 className="text-sm font-medium">{t('message.attachments')}</h3>
      <ul className="divide-y rounded-md border text-sm">
        {message.attachments.map(attachment => (
          <li key={attachment.id} className="flex items-center gap-3 px-3 py-2">
            <Paperclip className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span className="min-w-0 flex-1 truncate" title={attachment.filename}>
              {attachment.filename}
            </span>
            <span className="text-xs whitespace-nowrap text-muted-foreground">
              {formatBytes(attachment.size)}
            </span>
            <Button
              type="button"
              size="xs"
              isIcon
              variant="outline"
              loading={pending === attachment.id}
              disabled={pending !== null}
              icon={<Download className="size-3.5" />}
              aria-label={t('message.download', { name: attachment.filename })}
              onClick={() => void download(attachment)}
            />
          </li>
        ))}
      </ul>
    </section>
  );
}
