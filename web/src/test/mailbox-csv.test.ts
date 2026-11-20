import { describe, expect, it } from 'vitest';

import { parseCsv, readImportRows, toCsv } from '@/features/mailbox/utils/csv';
import {
  guardFormulaCell,
  parseImportMembers,
  stripFormulaGuard,
} from '@/features/mailbox/utils/mailbox-import';

describe('mailbox CSV files', () => {
  // Review Focus 5: quotes, escaped quotes, line breaks inside a cell, and the byte order mark.
  it('reads quoted cells with quotes and line breaks', () => {
    expect(
      parseCsv(
        '﻿address,display_name,members\r\n"x@d.test","Sales ""EU""\nTeam","a@p.test:send;b@p.test"\r\n'
      )
    ).toEqual([
      ['address', 'display_name', 'members'],
      ['x@d.test', 'Sales "EU"\nTeam', 'a@p.test:send;b@p.test'],
    ]);
  });

  it('adds no row for blank lines or a trailing line break', () => {
    expect(parseCsv('a,b\n\n\r\nc,d\n')).toEqual([
      ['a', 'b'],
      ['c', 'd'],
    ]);
    expect(parseCsv('')).toEqual([]);
    expect(parseCsv('a,,\n')).toEqual([['a', '', '']]);
  });

  it('writes CRLF lines and quotes only the cells that need it', () => {
    expect(toCsv([['a,b', 'c"d']])).toBe('"a,b","c""d"\r\n');
    expect(toCsv([['plain', 'two\nlines', '']])).toBe('plain,"two\nlines",\r\n');
    expect(parseCsv(toCsv([['a,b', 'c"d', 'x\r\ny']]))).toEqual([['a,b', 'c"d', 'x\r\ny']]);
  });

  it('reads import rows by header, in either language, ignoring other columns', () => {
    expect(
      readImportRows('error,Members, ADDRESS ,display_name\r\nold,a@p.test,x@d.test,X\r\n')
    ).toEqual({
      ok: true,
      rows: [{ address: 'x@d.test', display_name: 'X', members: 'a@p.test' }],
    });
    expect(readImportRows('邮箱地址,名称,成员\n销售@d.test,销售,a@p.test\n')).toEqual({
      ok: true,
      rows: [{ address: '销售@d.test', display_name: '销售', members: 'a@p.test' }],
    });
    // The display name column is optional.
    expect(readImportRows('address,members\nx@d.test,a@p.test\n')).toEqual({
      ok: true,
      rows: [{ address: 'x@d.test', display_name: '', members: 'a@p.test' }],
    });
  });

  it('refuses an empty file, missing columns, and more than 500 rows', () => {
    expect(readImportRows('')).toEqual({ ok: false, error: 'empty' });
    expect(readImportRows('address,display_name,members\r\n')).toEqual({
      ok: false,
      error: 'empty',
    });
    expect(readImportRows('address,display_name\nx@d.test,X\n')).toEqual({
      ok: false,
      error: 'missing_columns',
    });
    const body = Array.from({ length: 501 }, (_, index) => `b${index}@d.test,,a@p.test`).join('\n');
    expect(readImportRows(`address,display_name,members\n${body}\n`)).toEqual({
      ok: false,
      error: 'too_many_rows',
    });
    const fits = Array.from({ length: 500 }, (_, index) => `b${index}@d.test,,a@p.test`).join('\n');
    expect(readImportRows(`address,display_name,members\n${fits}\n`)).toMatchObject({ ok: true });
  });

  it('guards formula cells and reads members cells like the API', () => {
    expect(['=1', '+1', '-1', '@x', 'x', ''].map(guardFormulaCell)).toEqual([
      "'=1",
      "'+1",
      "'-1",
      "'@x",
      'x',
      '',
    ]);
    expect(["'=1", "'x", "''=1", "'"].map(stripFormulaGuard)).toEqual(['=1', "'x", "''=1", "'"]);
    expect(parseImportMembers(' a@p.test ; B@P.test:VIEW ;')).toEqual([
      { email: 'a@p.test', permission: 'send' },
      { email: 'b@p.test', permission: 'view' },
    ]);
    for (const cell of ['', ' ; ', 'a@p.test:owner', 'a@p.test;A@p.test', 'not-an-email']) {
      expect(parseImportMembers(cell)).toBeNull();
    }
  });
});
