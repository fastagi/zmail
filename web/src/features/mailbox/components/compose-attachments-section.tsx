'use client';

import { useId, useRef, type ChangeEvent } from 'react';
import { Paperclip, X } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { type ComposeDraft } from '@/features/mailbox/components/compose-drafts';
import { fileToBase64, formatBytes } from '@/features/mailbox/components/mail-attachments';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type { MailAttachment } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** Encodes forwarded and newly chosen attachments into base64 send payloads. */
export async function encodeComposeAttachments({
  organizationId,
  forwarded,
  carried,
  files,
}: {
  organizationId: number;
  forwarded: ComposeDraft['forwarded'];
  carried: readonly MailAttachment[];
  files: readonly File[];
}) {
  const forwardedPayloads = forwarded
    ? await Promise.all(
        carried.map(async attachment => ({
          filename: attachment.filename,
          ...(attachment.content_type ? { content_type: attachment.content_type } : {}),
          content: await fileToBase64(
            await mailboxService.fetchAttachment(
              organizationId,
              forwarded.mailboxId,
              forwarded.messageId,
              attachment
            )
          ),
        }))
      )
    : [];
  const chosenPayloads = await Promise.all(
    files.map(async file => ({
      filename: file.name,
      ...(file.type ? { content_type: file.type } : {}),
      content: await fileToBase64(file),
    }))
  );
  return [...forwardedPayloads, ...chosenPayloads];
}

export function ComposeAttachmentsSection({
  files,
  carried,
  lostFiles,
  restoringFiles,
  filesTooMany,
  filesTooLarge,
  disabled,
  onAddFiles,
  onRemoveCarried,
  onRemoveFile,
}: {
  files: readonly File[];
  carried: readonly MailAttachment[];
  lostFiles: readonly string[];
  restoringFiles: boolean;
  filesTooMany: boolean;
  filesTooLarge: boolean;
  disabled: boolean;
  onAddFiles: (event: ChangeEvent<HTMLInputElement>) => void;
  onRemoveCarried: (attachmentId: number) => void;
  onRemoveFile: (index: number) => void;
}) {
  const t = useT('mailbox');
  const fileInput = useRef<HTMLInputElement>(null);
  const attachHintId = useId();

  return (
    <div className="space-y-2">
      <input
        ref={fileInput}
        id="mail-compose-files"
        type="file"
        multiple
        className="sr-only"
        tabIndex={-1}
        aria-hidden="true"
        onChange={onAddFiles}
      />
      <div className="flex flex-wrap items-center gap-3">
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={disabled}
          icon={<Paperclip className="size-4" />}
          aria-describedby={attachHintId}
          onClick={() => fileInput.current?.click()}
        >
          {t('compose.attach')}
        </Button>
        <p
          id={attachHintId}
          className={cn(
            'text-xs',
            filesTooMany || filesTooLarge ? 'text-error' : 'text-muted-foreground'
          )}
        >
          {t('compose.attachHint')}
        </p>
      </div>
      {lostFiles.length > 0 && files.length === 0 && !restoringFiles ? (
        <p role="status" className="text-xs text-warning">
          {t('compose.draftFilesLost', { names: lostFiles.join(', ') })}
        </p>
      ) : null}
      {files.length + carried.length > 0 ? (
        <ul className="divide-y rounded-md border text-sm">
          {carried.map(attachment => (
            <li
              key={`forwarded-${String(attachment.id)}`}
              className="flex items-center gap-3 px-3 py-1.5"
            >
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
                variant="ghost"
                disabled={disabled}
                icon={<X className="size-3.5" />}
                aria-label={t('compose.removeAttachment', { name: attachment.filename })}
                onClick={() => onRemoveCarried(attachment.id)}
              />
            </li>
          ))}
          {files.map((file, index) => (
            // A list the user edits by position; two files may share a name.
            <li key={index} className="flex items-center gap-3 px-3 py-1.5">
              <Paperclip className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              <span className="min-w-0 flex-1 truncate" title={file.name}>
                {file.name}
              </span>
              <span className="text-xs whitespace-nowrap text-muted-foreground">
                {formatBytes(file.size)}
              </span>
              <Button
                type="button"
                size="xs"
                isIcon
                variant="ghost"
                disabled={disabled}
                icon={<X className="size-3.5" />}
                aria-label={t('compose.removeAttachment', { name: file.name })}
                onClick={() => onRemoveFile(index)}
              />
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
