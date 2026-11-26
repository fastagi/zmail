import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ComposeDialog, emptyDraft } from '@/features/mailbox/components/compose-dialog';
import { applyComposeFormat } from '@/features/mailbox/components/compose-format-toolbar';
import { setUndoSendDelayMs } from '@/features/mailbox/components/undo-send';
import { MAIL_ATTACHMENTS_MAX_BYTES, MAIL_MAX_ATTACHMENTS } from '@/features/mailbox/schemas';
import { messages } from '@/i18n/modules';

const state = vi.hoisted(() => ({
  send: {
    isPending: false,
    mutateAsync: vi.fn(),
  },
  saveDraft: {
    isPending: false,
    mutateAsync: vi.fn(),
  },
  deleteDraft: {
    isPending: false,
    mutateAsync: vi.fn(),
  },
  toast: Object.assign(vi.fn(), {
    success: vi.fn(),
    error: vi.fn(),
  }),
}));

vi.mock('sonner', () => ({ toast: state.toast }));
vi.mock('@/features/mailbox/components/mail-organization', () => ({
  useMailOrganizationId: () => 1,
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useSendMailMessage: () => state.send,
}));
vi.mock('@/features/mailbox/hooks/use-mail-organization', () => ({
  useChosenMemberFeatures: () => ({ templates: false }),
}));
vi.mock('@/features/mailbox/hooks/use-mail-templates', () => ({
  useMailTemplates: () => ({ data: [] }),
}));
vi.mock('@/features/mailbox/hooks/use-mail-signature', () => ({
  useMailSignature: () => ({ data: undefined }),
}));
vi.mock('@/features/mailbox/hooks/use-mail-drafts', () => ({
  useSaveMailDraft: () => state.saveDraft,
  useDeleteMailDraft: () => state.deleteDraft,
}));

function renderComposer() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const onClose = vi.fn();
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <ComposeDialog
          mailbox={{ id: 1, address: 'hello@mail.example.com' }}
          draft={emptyDraft}
          onClose={onClose}
        />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
  const dialog = screen.getByRole('dialog', { name: 'New message' });
  const form = dialog.querySelector('form')!;
  const body = within(dialog).getByLabelText('Message') as HTMLTextAreaElement;
  return { ...utils, dialog, form, body, onClose };
}

describe('applyComposeFormat pure transforms', () => {
  it('wraps and toggles bold, italic, and inline/fenced code while preserving selection', () => {
    const bolded = applyComposeFormat('Hello world', 6, 11, 'bold');
    expect(bolded).toEqual({ value: 'Hello **world**', selectionStart: 8, selectionEnd: 13 });

    const unbolded = applyComposeFormat(
      bolded.value,
      bolded.selectionStart,
      bolded.selectionEnd,
      'bold'
    );
    expect(unbolded).toEqual({ value: 'Hello world', selectionStart: 6, selectionEnd: 11 });

    // Applying italic inside bold wraps with a third asterisk instead of stripping bold.
    const boldItalic = applyComposeFormat(
      bolded.value,
      bolded.selectionStart,
      bolded.selectionEnd,
      'italic'
    );
    expect(boldItalic).toEqual({ value: 'Hello ***world***', selectionStart: 9, selectionEnd: 14 });

    const inlineCode = applyComposeFormat('Run npm test now', 4, 12, 'code');
    expect(inlineCode).toEqual({
      value: 'Run `npm test` now',
      selectionStart: 5,
      selectionEnd: 13,
    });

    const fencedCode = applyComposeFormat('line1\nline2', 0, 11, 'code');
    expect(fencedCode).toEqual({
      value: '```\nline1\nline2\n```',
      selectionStart: 4,
      selectionEnd: 15,
    });
  });

  it('applies list, quote, and link transforms on single lines, multi-lines, and empty selections', () => {
    expect(applyComposeFormat('first\nsecond', 0, 12, 'list')).toEqual({
      value: '- first\n- second',
      selectionStart: 2,
      selectionEnd: 16,
    });

    expect(applyComposeFormat('- first\n- second', 2, 16, 'list')).toEqual({
      value: 'first\nsecond',
      selectionStart: 0,
      selectionEnd: 12,
    });

    expect(applyComposeFormat('quoted line', 0, 11, 'quote')).toEqual({
      value: '> quoted line',
      selectionStart: 2,
      selectionEnd: 13,
    });

    expect(applyComposeFormat('See docs here', 4, 8, 'link')).toEqual({
      value: 'See [docs](https://) here',
      selectionStart: 11,
      selectionEnd: 19,
    });

    expect(applyComposeFormat('https://zmail.example.com', 0, 25, 'link')).toEqual({
      value: '[](https://zmail.example.com)',
      selectionStart: 1,
      selectionEnd: 1,
    });

    expect(applyComposeFormat('', 0, 0, 'bold')).toEqual({
      value: '****',
      selectionStart: 2,
      selectionEnd: 2,
    });
  });
});

describe('Compose formatting toolbar, shortcuts, clipboard paste & drag-and-drop', () => {
  beforeEach(() => {
    window.localStorage.clear();
    setUndoSendDelayMs(0);
    state.send.isPending = false;
    state.send.mutateAsync.mockReset();
    state.send.mutateAsync.mockResolvedValue({ id: 10, queued_reason: null });
    state.saveDraft.mutateAsync.mockReset();
    state.saveDraft.mutateAsync.mockResolvedValue({ id: 99 });
    state.deleteDraft.mutateAsync.mockReset();
    state.deleteDraft.mutateAsync.mockResolvedValue(undefined);
    state.toast.mockReset();
    state.toast.success.mockReset();
    state.toast.error.mockReset();
  });

  it('formats textarea selections via toolbar buttons and preserves cursor/selection', () => {
    const { dialog, body } = renderComposer();
    const toolbar = within(dialog).getByRole('toolbar', { name: 'Formatting' });

    fireEvent.change(body, { target: { value: 'Hello world' } });
    body.setSelectionRange(6, 11);
    fireEvent.click(within(toolbar).getByRole('button', { name: 'Bold' }));
    expect(body).toHaveValue('Hello **world**');
    expect(body.selectionStart).toBe(8);
    expect(body.selectionEnd).toBe(13);

    fireEvent.change(body, { target: { value: 'Check docs' } });
    body.setSelectionRange(6, 10);
    fireEvent.click(within(toolbar).getByRole('button', { name: 'Link' }));
    expect(body).toHaveValue('Check [docs](https://)');
    expect(body.selectionStart).toBe(13);
    expect(body.selectionEnd).toBe(21);

    fireEvent.change(body, { target: { value: 'item one\nitem two' } });
    body.setSelectionRange(0, 17);
    fireEvent.click(within(toolbar).getByRole('button', { name: 'Bulleted list' }));
    expect(body).toHaveValue('- item one\n- item two');

    fireEvent.change(body, { target: { value: 'Key takeaway' } });
    body.setSelectionRange(0, 12);
    fireEvent.click(within(toolbar).getByRole('button', { name: 'Quote' }));
    expect(body).toHaveValue('> Key takeaway');

    fireEvent.change(body, { target: { value: 'const x = 1' } });
    body.setSelectionRange(0, 11);
    fireEvent.click(within(toolbar).getByRole('button', { name: 'Code' }));
    expect(body).toHaveValue('`const x = 1`');
  });

  it('supports Cmd/Ctrl+B and Cmd/Ctrl+I shortcuts inside the body textarea', () => {
    const { body } = renderComposer();

    fireEvent.change(body, { target: { value: 'Release notes' } });
    body.setSelectionRange(0, 7);
    fireEvent.keyDown(body, { key: 'b', metaKey: true });
    expect(body).toHaveValue('**Release** notes');
    expect(body.selectionStart).toBe(2);
    expect(body.selectionEnd).toBe(9);

    fireEvent.keyDown(body, { key: 'i', ctrlKey: true });
    expect(body).toHaveValue('***Release*** notes');
    expect(body.selectionStart).toBe(3);
    expect(body.selectionEnd).toBe(10);
  });

  it('attaches pasted clipboard images with clean filenames, toasts confirmation, and leaves text paste untouched', () => {
    const { dialog, body } = renderComposer();

    // Plain text paste is not intercepted and does not attach files or toast.
    const textPasteEvent = new Event('paste', { bubbles: true, cancelable: true });
    Object.defineProperty(textPasteEvent, 'clipboardData', {
      value: {
        files: [],
        items: [],
        getData: () => 'Plain copied text',
      },
    });
    fireEvent(body, textPasteEvent);
    expect(textPasteEvent.defaultPrevented).toBe(false);
    expect(state.toast.success).not.toHaveBeenCalled();

    // Unnamed screenshot image paste gets named `pasted-image-1.png`.
    const screenshot1 = new File(['png-bytes-1'], '', { type: 'image/png' });
    const imagePaste1 = new Event('paste', { bubbles: true, cancelable: true });
    Object.defineProperty(imagePaste1, 'clipboardData', {
      value: { files: [screenshot1], items: [] },
    });
    fireEvent(body, imagePaste1);

    expect(imagePaste1.defaultPrevented).toBe(true);
    expect(within(dialog).getByText('pasted-image-1.png')).toBeInTheDocument();
    expect(state.toast.success).toHaveBeenCalledWith('Attached pasted-image-1.png');

    // Second unnamed image paste increments to `pasted-image-2.jpg`.
    const screenshot2 = new File(['jpg-bytes-2'], '', { type: 'image/jpeg' });
    fireEvent.paste(body, { clipboardData: { files: [screenshot2], items: [] } });
    expect(within(dialog).getByText('pasted-image-2.jpg')).toBeInTheDocument();
    expect(state.toast.success).toHaveBeenCalledWith('Attached pasted-image-2.jpg');
  });

  it('shows a drop overlay on file drag-over, hides on drag-leave, and attaches dropped files', () => {
    const { dialog, form } = renderComposer();

    expect(dialog.querySelector('[data-compose-drop-overlay="true"]')).toBeNull();

    fireEvent.dragOver(form, {
      dataTransfer: { types: ['Files'], files: [], items: [], dropEffect: 'none' },
    });
    expect(dialog.querySelector('[data-compose-drop-overlay="true"]')).toBeInTheDocument();
    expect(within(dialog).getByText('Drop files to attach')).toBeInTheDocument();

    fireEvent.dragLeave(form, { relatedTarget: null });
    expect(dialog.querySelector('[data-compose-drop-overlay="true"]')).toBeNull();

    const droppedPdf = new File(['pdf-content'], 'roadmap.pdf', { type: 'application/pdf' });
    const droppedPng = new File(['png-content'], 'diagram.png', { type: 'image/png' });
    fireEvent.dragOver(form, {
      dataTransfer: { types: ['Files'], files: [droppedPdf, droppedPng], items: [] },
    });
    fireEvent.drop(form, {
      dataTransfer: { types: ['Files'], files: [droppedPdf, droppedPng], items: [] },
    });

    expect(dialog.querySelector('[data-compose-drop-overlay="true"]')).toBeNull();
    expect(within(dialog).getByText('roadmap.pdf')).toBeInTheDocument();
    expect(within(dialog).getByText('diagram.png')).toBeInTheDocument();
    expect(state.toast.success).toHaveBeenCalledWith('Attached 2 files');
  });

  it('enforces MAIL_MAX_ATTACHMENTS and MAIL_ATTACHMENTS_MAX_BYTES for pasted and dropped files', () => {
    const { dialog, form, body } = renderComposer();

    // Oversized pasted image (> MAIL_ATTACHMENTS_MAX_BYTES) is rejected.
    const huge = new File([new Uint8Array(MAIL_ATTACHMENTS_MAX_BYTES + 1)], 'huge.png', {
      type: 'image/png',
    });
    fireEvent.paste(body, { clipboardData: { files: [huge], items: [] } });
    expect(within(dialog).queryByText('huge.png')).toBeNull();
    expect(state.toast.error).toHaveBeenCalledWith(
      'Attach at most 10 non-empty files, 4 MB in total.'
    );
    expect(
      within(dialog).getByText('Attach at most 10 non-empty files, 4 MB in total.')
    ).toBeInTheDocument();

    // Dropping more than MAIL_MAX_ATTACHMENTS files is also rejected.
    state.toast.error.mockReset();
    const tooMany = Array.from(
      { length: MAIL_MAX_ATTACHMENTS + 1 },
      (_, idx) => new File(['ok'], `file-${idx + 1}.txt`, { type: 'text/plain' })
    );
    fireEvent.drop(form, {
      dataTransfer: { types: ['Files'], files: tooMany, items: [] },
    });
    expect(within(dialog).queryByText('file-1.txt')).toBeNull();
    expect(state.toast.error).toHaveBeenCalledWith(
      'Attach at most 10 non-empty files, 4 MB in total.'
    );
  });
});
