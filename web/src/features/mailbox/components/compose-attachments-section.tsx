'use client';

import {
  useId,
  useRef,
  useState,
  type ChangeEvent,
  type ClipboardEvent,
  type Dispatch,
  type DragEvent,
  type SetStateAction,
} from 'react';
import { Paperclip, Upload, X } from 'lucide-react';
import { useLocale } from 'next-intl';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { type ComposeDraft } from '@/features/mailbox/components/compose-drafts';
import { fileToBase64, formatBytes } from '@/features/mailbox/components/mail-attachments';
import { MAIL_ATTACHMENTS_MAX_BYTES, MAIL_MAX_ATTACHMENTS } from '@/features/mailbox/schemas';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type { MailAttachment } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const IMAGE_EXTENSION_BY_MIME: Record<string, string> = {
  'image/png': 'png',
  'image/jpeg': 'jpg',
  'image/jpg': 'jpg',
  'image/gif': 'gif',
  'image/webp': 'webp',
  'image/svg+xml': 'svg',
  'image/bmp': 'bmp',
  'image/avif': 'avif',
};

const PASTED_IMAGE_NAME_PATTERN = /^pasted-image-(\d+)\./iu;

function extensionForImageMime(mimeType: string): string {
  const normalized = mimeType.toLowerCase().split(';')[0]?.trim() ?? '';
  return IMAGE_EXTENSION_BY_MIME[normalized] ?? 'png';
}

function nextPastedImageIndex(existingFiles: readonly File[]): number {
  let maxIndex = 0;
  for (const file of existingFiles) {
    const match = PASTED_IMAGE_NAME_PATTERN.exec(file.name);
    if (match) {
      maxIndex = Math.max(maxIndex, Number(match[1]));
    }
  }
  return maxIndex + 1;
}

/** Extracts pasted image files from clipboard data; ignores plain-text clipboard pastes. */
export function extractPastedImageFiles(
  clipboardData: Pick<DataTransfer, 'files' | 'items'> | null | undefined
): File[] {
  if (!clipboardData) return [];
  const fromFiles = Array.from(clipboardData.files ?? []).filter(file =>
    file.type.toLowerCase().startsWith('image/')
  );
  if (fromFiles.length > 0) return fromFiles;

  const fromItems: File[] = [];
  for (const item of Array.from(clipboardData.items ?? [])) {
    if (item.kind === 'file' && item.type.toLowerCase().startsWith('image/')) {
      const file = item.getAsFile();
      if (file) fromItems.push(file);
    }
  }
  return fromItems;
}

/** Assigns a clean `pasted-image-N.<ext>` filename when a pasted image has no usable name. */
export function preparePastedImageFiles(
  images: readonly File[],
  existingFiles: readonly File[]
): File[] {
  let index = nextPastedImageIndex(existingFiles);
  return images.map(file => {
    const trimmed = file.name.trim();
    if (trimmed !== '' && trimmed.toLowerCase() !== 'blob') return file;
    const ext = extensionForImageMime(file.type);
    const filename = `pasted-image-${index}.${ext}`;
    index += 1;
    return new File([file], filename, {
      type: file.type || 'image/png',
      lastModified: file.lastModified || Date.now(),
    });
  });
}

/** True when a drag event carries files rather than plain text or UI elements. */
export function hasDraggedFiles(
  dataTransfer: Pick<DataTransfer, 'types' | 'files' | 'items'> | null | undefined
): boolean {
  if (!dataTransfer) return false;
  if (Array.from(dataTransfer.types ?? []).includes('Files')) return true;
  if ((dataTransfer.files?.length ?? 0) > 0) return true;
  return Array.from(dataTransfer.items ?? []).some(item => item.kind === 'file');
}

export type AttachmentValidationFailure = 'too_many' | 'too_large' | 'empty';

export function validateComposeAttachments(
  existingFiles: readonly File[],
  carried: readonly MailAttachment[],
  incoming: readonly File[]
): { ok: true } | { ok: false; reason: AttachmentValidationFailure } {
  if (incoming.some(file => file.size === 0)) {
    return { ok: false, reason: 'empty' };
  }
  if (existingFiles.length + carried.length + incoming.length > MAIL_MAX_ATTACHMENTS) {
    return { ok: false, reason: 'too_many' };
  }
  const totalBytes =
    existingFiles.reduce((sum, file) => sum + file.size, 0) +
    carried.reduce((sum, file) => sum + file.size, 0) +
    incoming.reduce((sum, file) => sum + file.size, 0);
  if (totalBytes > MAIL_ATTACHMENTS_MAX_BYTES) {
    return { ok: false, reason: 'too_large' };
  }
  return { ok: true };
}

function formatAttachedToast(files: readonly File[], source: 'paste' | 'drop', isZh: boolean) {
  if (files.length === 1) {
    return isZh ? `已添加附件：${files[0].name}` : `Attached ${files[0].name}`;
  }
  if (source === 'paste') {
    return isZh ? `已粘贴 ${files.length} 张图片` : `Attached ${files.length} pasted images`;
  }
  return isZh ? `已添加 ${files.length} 个附件` : `Attached ${files.length} files`;
}

/**
 * Owns compose attachment limit checks, file-picker additions, clipboard image paste, and
 * drag-and-drop file attachment state for the composer form.
 */
export function useComposeAttachmentsInput({
  files,
  carried,
  disabled,
  setFiles,
  setLostFiles,
  onClearError,
  onInvalidAttachments,
}: {
  files: readonly File[];
  carried: readonly MailAttachment[];
  disabled: boolean;
  setFiles: Dispatch<SetStateAction<File[]>>;
  setLostFiles: Dispatch<SetStateAction<string[]>>;
  onClearError: () => void;
  onInvalidAttachments: () => void;
}) {
  const t = useT('mailbox');
  const locale = useLocale();
  const isZh = locale.toLowerCase().startsWith('zh');
  const [isDragOver, setIsDragOver] = useState(false);

  const filesTooMany = files.length + carried.length > MAIL_MAX_ATTACHMENTS;
  const filesTooLarge =
    files.reduce((total, file) => total + file.size, 0) +
      carried.reduce((total, file) => total + file.size, 0) >
    MAIL_ATTACHMENTS_MAX_BYTES;

  const appendValidatedFiles = (incoming: readonly File[], source: 'paste' | 'drop') => {
    if (disabled || incoming.length === 0) return;
    const prepared = source === 'paste' ? preparePastedImageFiles(incoming, files) : [...incoming];
    const check = validateComposeAttachments(files, carried, prepared);
    if (!check.ok) {
      onInvalidAttachments();
      toast.error(t('errors.attachmentsInvalid'));
      return;
    }
    onClearError();
    setFiles(current => [...current, ...prepared]);
    setLostFiles([]);
    toast.success(formatAttachedToast(prepared, source, isZh));
  };

  const addFiles = (event: ChangeEvent<HTMLInputElement>) => {
    const chosen = Array.from(event.target.files ?? []);
    // Clear the input so the same file can be chosen again after it is removed.
    event.target.value = '';
    onClearError();
    setFiles(current => [...current, ...chosen]);
    // Attaching anything settles the reminder; removing files later does not bring it back.
    setLostFiles([]);
  };

  const handlePaste = (event: ClipboardEvent<HTMLFormElement>) => {
    if (disabled) return;
    const images = extractPastedImageFiles(event.clipboardData);
    if (images.length === 0) return;
    event.preventDefault();
    appendValidatedFiles(images, 'paste');
  };

  const handleDragOver = (event: DragEvent<HTMLFormElement>) => {
    if (disabled || !hasDraggedFiles(event.dataTransfer)) return;
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
    if (!isDragOver) setIsDragOver(true);
  };

  const handleDragLeave = (event: DragEvent<HTMLFormElement>) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      setIsDragOver(false);
    }
  };

  const handleDrop = (event: DragEvent<HTMLFormElement>) => {
    setIsDragOver(false);
    if (disabled || !hasDraggedFiles(event.dataTransfer)) return;
    event.preventDefault();
    const dropped = Array.from(event.dataTransfer?.files ?? []);
    if (dropped.length > 0) appendValidatedFiles(dropped, 'drop');
  };

  return {
    filesTooMany,
    filesTooLarge,
    isDragOver,
    addFiles,
    formAttachmentProps: {
      onPaste: handlePaste,
      onDragOver: handleDragOver,
      onDragLeave: handleDragLeave,
      onDrop: handleDrop,
    },
  };
}

/** Subtle dashed brand drop overlay rendered over the compose form while dragging files. */
export function ComposeDropOverlay() {
  const t = useT('mailbox');
  const locale = useLocale();
  const isZh = locale.toLowerCase().startsWith('zh');

  return (
    <div
      data-compose-drop-overlay="true"
      aria-live="polite"
      className="pointer-events-none absolute inset-2 z-20 flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed border-primary/60 bg-primary/5 p-6 text-center backdrop-blur-[2px]"
    >
      <div className="flex size-10 items-center justify-center rounded-full bg-primary/10 text-primary">
        <Upload className="size-5" aria-hidden="true" />
      </div>
      <p className="text-sm font-medium text-foreground">
        {isZh ? '松开以添加附件' : 'Drop files to attach'}
      </p>
      <p className="text-xs text-muted-foreground">{t('compose.attachHint')}</p>
    </div>
  );
}

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
