import { beforeEach, describe, expect, it } from 'vitest';

import {
  composeDraftFilesKept,
  draftToKeep,
  forgetComposeDraftPageFiles,
  loadComposeDraftFiles,
  readComposeDraft,
  readComposeDraftFiles,
  setComposeDraftFileStore,
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

  it('reads files back after a reload only while the kept draft lists exactly them', async () => {
    const kept = new Map<string, File[]>();
    const previous = setComposeDraftFileStore({
      get: key => Promise.resolve(kept.get(key) ?? null),
      put: (key, files) => {
        kept.set(key, [...files]);
        return Promise.resolve(true);
      },
      remove: key => {
        kept.delete(key);
        return Promise.resolve();
      },
    });
    try {
      const file = new File(['x'], 'a.txt');
      writeComposeDraft('k', { ...blank, attachments: ['a.txt'] });
      writeComposeDraftFiles('k', [file]);
      await expect(composeDraftFilesKept('k')).resolves.toBe(true);

      forgetComposeDraftPageFiles();
      expect(readComposeDraftFiles('k')).toEqual([]);
      await expect(loadComposeDraftFiles('k')).resolves.toEqual([file]);

      forgetComposeDraftPageFiles();
      writeComposeDraft('k', { ...blank, attachments: ['b.txt'] });
      await expect(loadComposeDraftFiles('k')).resolves.toEqual([]);
    } finally {
      setComposeDraftFileStore(previous);
    }
  });

  it('says files stay only in the page when the browser keeps none', async () => {
    writeComposeDraft('k', { ...blank, attachments: ['a.txt'] });
    writeComposeDraftFiles('k', [new File(['x'], 'a.txt')]);
    await expect(composeDraftFilesKept('k')).resolves.toBe(false);
  });
});
