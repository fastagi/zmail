import type { z } from 'zod/mini';

import {
  MAILBOX_DIRECTORY_CURSOR_MAX_LENGTH,
  MAILBOX_DIRECTORY_QUERY_MAX_LENGTH,
  MAILBOX_IMPORT_MAX_ROWS,
  MEMBER_IMPORT_MAX_ROWS,
  mailboxDirectoryPageEnvelopeSchema,
  mailboxImportResultSchema,
  mailboxSchema,
  memberImportResultSchema,
} from '@/features/mailbox/schemas';
import type {
  Mailbox,
  MailboxDirectoryFilters,
  MailboxDirectoryPage,
  MailboxImportResult,
  MailboxImportRowInput,
  MailboxType,
  MemberImportFeatures,
  MemberImportInput,
  MemberImportResult,
  MemberImportRowInput,
} from '@/features/mailbox/types';
import { ClientErrorCode } from '@/http/codes';
import {
  assertContract,
  type Accepts,
  type ContractData,
  type ContractRequest,
  type ContractResponse,
  type Sends,
} from '@/http/contract';
import request, { ApiError } from '@/http/request';

/**
 * Browser service for the mailbox administration routes of `contracts/MAILBOXES.md` (Mailbox
 * Directory, Import And Export). It is separate from `mailboxService` so the mail pages do not load
 * code only the admin area uses.
 */
export const mailboxAdminService = {
  /** One page of the administration directory, newest first; owners and admins only. */
  async directory(
    organizationId: number,
    filters: MailboxDirectoryFilters,
    cursor?: string
  ): Promise<MailboxDirectoryPage> {
    const headers = organizationHeaders(organizationId);
    const q = filters.q?.trim();
    if (q !== undefined && Array.from(q).length > MAILBOX_DIRECTORY_QUERY_MAX_LENGTH) {
      throw invalidResponse();
    }
    if (
      cursor !== undefined &&
      (cursor === '' || cursor.length > MAILBOX_DIRECTORY_CURSOR_MAX_LENGTH)
    ) {
      throw invalidResponse();
    }
    const value = await request.getEnvelope<unknown>('/mailbox-directory', {
      headers,
      params: {
        ...(filters.type ? { type: filters.type } : {}),
        ...(q ? { q } : {}),
        ...(cursor === undefined ? {} : { cursor }),
      },
    });
    const parsed = mailboxDirectoryPageEnvelopeSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return { items: parsed.data.data, meta: parsed.data.meta };
  },

  /** Changes a mailbox's display name; an empty name clears it. Owners and admins only. */
  async rename(organizationId: number, mailboxId: number, displayName: string): Promise<Mailbox> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    const parsed = mailboxSchema.safeParse(
      await request.put<unknown, { display_name: string }>(
        `/mailboxes/${mailboxId}`,
        { display_name: displayName },
        { headers }
      )
    );
    if (!parsed.success || parsed.data.id !== mailboxId) throw invalidResponse();
    return parsed.data;
  },

  /** Checks (`dryRun`) or creates shared mailboxes from spreadsheet rows, row by row. */
  async importMailboxes(
    organizationId: number,
    rows: MailboxImportRowInput[],
    dryRun: boolean
  ): Promise<MailboxImportResult> {
    const headers = organizationHeaders(organizationId);
    if (rows.length === 0 || rows.length > MAILBOX_IMPORT_MAX_ROWS) throw invalidResponse();
    const parsed = mailboxImportResultSchema.safeParse(
      await request.post<unknown, { dry_run: boolean; rows: MailboxImportRowInput[] }>(
        '/mailbox-imports',
        { dry_run: dryRun, rows },
        { headers }
      )
    );
    if (!parsed.success || parsed.data.rows.length !== rows.length) throw invalidResponse();
    return parsed.data;
  },

  /**
   * Checks (`dryRun`) or applies a roster import: invites each row, reserves each a personal
   * mailbox on `domain`, and starts each invitation with `features`. Owners and admins only.
   */
  async importMembers(organizationId: number, input: MemberImportInput): Promise<MemberImportResult> {
    const headers = organizationHeaders(organizationId);
    const { rows } = input;
    if (rows.length === 0 || rows.length > MEMBER_IMPORT_MAX_ROWS) throw invalidResponse();
    const parsed = memberImportResultSchema.safeParse(
      await request.post<unknown, MemberImportBody>(
        '/member-imports',
        { dry_run: input.dryRun, domain: input.domain, features: input.features, rows },
        { headers }
      )
    );
    if (!parsed.success || parsed.data.rows.length !== rows.length) throw invalidResponse();
    return parsed.data;
  },

  /** The CSV file of every mailbox of one type, with the name the API gave it. */
  async exportMailboxes(
    organizationId: number,
    type: MailboxType
  ): Promise<{ blob: Blob; filename: string }> {
    const headers = organizationHeaders(organizationId);
    const file = await request.getFile('/mailbox-exports', { headers, params: { type } });
    if (!(file.blob instanceof Blob)) throw invalidResponse();
    return {
      blob: file.blob,
      filename: dispositionFilename(file.disposition) ?? `${type}-mailboxes.csv`,
    };
  },
};

interface MemberImportBody {
  dry_run: boolean;
  domain: string;
  features: MemberImportFeatures;
  rows: MemberImportRowInput[];
}

function dispositionFilename(disposition: string | null): string | null {
  const match = /^attachment;\s*filename="([A-Za-z0-9._-]{1,128})"$/u.exec(disposition ?? '');
  return match?.[1] ?? null;
}

function organizationHeaders(organizationId: number): Record<string, string> {
  validId(organizationId);
  return { 'Organization-Id': String(organizationId) };
}

function validId(value: number): void {
  if (!Number.isSafeInteger(value) || value < 1) throw invalidResponse();
}

function invalidResponse(): ApiError {
  return new ApiError(
    'Mailbox admin service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}

assertContract<
  Accepts<
    z.input<typeof mailboxDirectoryPageEnvelopeSchema>,
    ContractResponse<'listMailboxDirectory', 200>
  >
>();
assertContract<Accepts<z.input<typeof mailboxSchema>, ContractData<'updateMailbox', 200>>>();
assertContract<Sends<{ display_name: string }, ContractRequest<'updateMailbox'>>>();
assertContract<
  Accepts<z.input<typeof mailboxImportResultSchema>, ContractData<'importMailboxes', 200>>
>();
assertContract<
  Sends<{ dry_run: boolean; rows: MailboxImportRowInput[] }, ContractRequest<'importMailboxes'>>
>();
assertContract<
  Accepts<z.input<typeof memberImportResultSchema>, ContractData<'importMembers', 200>>
>();
assertContract<Sends<MemberImportBody, ContractRequest<'importMembers'>>>();
