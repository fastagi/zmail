import { beforeEach, describe, expect, it } from 'vitest';

import {
  draftToKeep,
  readComposeDraft,
  readComposeDraftFiles,
  writeComposeDraft,
  writeComposeDraftFiles,
} from '@/features/mailbox/components/compose-draft-store';
import { emptyDraft } from '@/features/mailbox/components/compose-drafts';

const blank = { to: '', cc: '', subject: '', body: '' };

describe('compose draft store', () => {
  beforeEach(() => {
    window.localStorage.clear();
    writeComposeDraftFiles('k', null);
  });

  it('keeps a draft that holds only attached files', () => {
    const written = { ...blank, attachments: ['a.txt'] };
    expect(draftToKeep(written, emptyDraft)).toEqual(written);
    expect(draftToKeep(blank, emptyDraft)).toBeNull();
  });

  it('remembers attachment names across a reload but not the files', () => {
    writeComposeDraft('k', { ...blank, subject: 'Hi', attachments: ['a.txt', 'b.pdf'] });
    expect(readComposeDraft('k')?.attachments).toEqual(['a.txt', 'b.pdf']);
    expect(readComposeDraftFiles('k')).toEqual([]);
  });

  it('keeps files for the page while the kept draft lists them', () => {
    const file = new File(['x'], 'a.txt');
    writeComposeDraft('k', { ...blank, attachments: ['a.txt'] });
    writeComposeDraftFiles('k', [file]);
    expect(readComposeDraftFiles('k')).toEqual([file]);
    writeComposeDraftFiles('k', []);
    expect(readComposeDraftFiles('k')).toEqual([]);
  });

  it('drops the files once the kept draft is gone', () => {
    writeComposeDraft('k', { ...blank, attachments: ['a.txt'] });
    writeComposeDraftFiles('k', [new File(['x'], 'a.txt')]);
    writeComposeDraft('k', null);
    expect(readComposeDraftFiles('k')).toEqual([]);
  });

  it('ignores a malformed attachment list', () => {
    window.localStorage.setItem('k', JSON.stringify({ ...blank, attachments: [1] }));
    expect(readComposeDraft('k')).toEqual(blank);
  });
});
