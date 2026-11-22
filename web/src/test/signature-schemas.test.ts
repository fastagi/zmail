import { describe, expect, it } from 'vitest';

import {
  mailSignatureBlock,
  mailSignatureSchema,
  normalizeMailSignature,
} from '@/features/mailbox/signature-schemas';

describe('mail signature schema', () => {
  it('counts characters, not UTF-16 units, against the limit', () => {
    const parse = (body: string) =>
      mailSignatureSchema.safeParse({ mailbox_id: 1, body, updated_at: null }).success;
    expect(parse('😀'.repeat(2000))).toBe(true);
    expect(parse('😀'.repeat(2001))).toBe(false);
  });

  it('normalizes and builds the composer block', () => {
    expect(normalizeMailSignature('Best,\r\nAda \n')).toBe('Best,\nAda');
    expect(mailSignatureBlock('Ada')).toBe('\n\n-- \nAda');
    expect(mailSignatureBlock('  ')).toBe('');
    expect(mailSignatureBlock(undefined)).toBe('');
  });
});
