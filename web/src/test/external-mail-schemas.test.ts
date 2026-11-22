import { describe, expect, it } from 'vitest';

import {
  externalMailConnectionSchema,
  externalMailDirectoryEntrySchema,
} from '@/features/mailbox/external-mail-schemas';

// The API always sends `tenant_id` on a connection and on a directory entry; these are the exact
// shapes of `ExternalMailConnectionResponse` and `ExternalMailConnectionDirectoryEntryResponse`.
const connection = {
  id: 7,
  provider: 'gmail',
  account_address: 'alice@example.com',
  mailbox_id: 12,
  shared: false,
  tenant_id: null,
  status: 'active',
  last_synced_at: null,
  last_error_code: null,
  created_at: '2026-11-22T08:00:00Z',
};

const directoryEntry = {
  id: 7,
  provider: 'gmail',
  status: 'active',
  shared: false,
  tenant_id: null,
  account_address: null,
  mailbox_id: null,
  masked_account_address: 'a***@example.com',
  owner_user_id: 3,
  owner_display_name: 'Alice',
  last_synced_at: null,
  created_at: '2026-11-22T08:00:00Z',
};

describe('external mail response schemas', () => {
  it('parses a connection as the API sends it, with and without a tenant', () => {
    expect(externalMailConnectionSchema.safeParse(connection).success).toBe(true);
    expect(externalMailConnectionSchema.safeParse({ ...connection, tenant_id: 4 }).success).toBe(
      true
    );
  });

  it('parses a directory entry as the API sends it, with and without a tenant', () => {
    expect(externalMailDirectoryEntrySchema.safeParse(directoryEntry).success).toBe(true);
    expect(
      externalMailDirectoryEntrySchema.safeParse({ ...directoryEntry, tenant_id: 4 }).success
    ).toBe(true);
  });

  it('still parses responses from an API without tenants', () => {
    const { tenant_id: _connectionTenant, ...oldConnection } = connection;
    const { tenant_id: _entryTenant, ...oldEntry } = directoryEntry;
    expect(externalMailConnectionSchema.safeParse(oldConnection).success).toBe(true);
    expect(externalMailDirectoryEntrySchema.safeParse(oldEntry).success).toBe(true);
  });
});
