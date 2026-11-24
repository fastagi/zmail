import type { z } from 'zod/mini';

import {
  mailboxDomainListSchema,
  mailDnsRecordListSchema,
  mailDnsRecordSchema,
  mailDomainListSchema,
  mailDomainPreviewSchema,
  mailDomainSchema,
  mailZoneListSchema,
  mailZoneSchema,
} from '@/features/mailbox/schemas';
import type {
  MailboxDomain,
  MailDnsRecord,
  MailDnsRecordInput,
  MailDnsRecordPatch,
  MailDomain,
  MailDomainInput,
  MailDomainPreview,
  MailDomainPreviewInput,
  MailZone,
} from '@/features/mailbox/types';
import {
  assertContract,
  type Accepts,
  type ContractData,
  type ContractRequest,
  type Sends,
} from '@/http/contract';
import request from '@/http/request';

import {
  invalidResponse,
  organizationHeaders,
  validId,
  validRecordId,
} from './mailbox-service-shared';

/** Domain, zone, and DNS record routes for mailboxes. */
export const mailDomainService = {
  async domains(organizationId: number): Promise<MailboxDomain[]> {
    const parsed = mailboxDomainListSchema.safeParse(
      await request.get<unknown>('/mailbox-domains', {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Domains the organization connected, with the records of their last check. */
  async mailDomains(organizationId: number): Promise<MailDomain[]> {
    const parsed = mailDomainListSchema.safeParse(
      await request.get<unknown>('/mail-domains', { headers: organizationHeaders(organizationId) })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async connectMailDomain(organizationId: number, input: MailDomainInput): Promise<MailDomain> {
    return parseMailDomain(
      await request.post<unknown, MailDomainInput>('/mail-domains', input, {
        headers: organizationHeaders(organizationId),
      })
    );
  },

  /** Runs the setup again (`setups`) or only re-reads DNS (`checks`). */
  async refreshMailDomain(
    organizationId: number,
    domainId: number,
    action: 'setups' | 'checks'
  ): Promise<MailDomain> {
    validId(domainId);
    return parseMailDomain(
      await request.post<unknown>(`/mail-domains/${domainId}/${action}`, undefined, {
        headers: organizationHeaders(organizationId),
      }),
      domainId
    );
  },

  async removeMailDomain(organizationId: number, domainId: number): Promise<void> {
    validId(domainId);
    await request.delete<void>(`/mail-domains/${domainId}`, {
      headers: organizationHeaders(organizationId),
    });
  },

  /** What connecting the domain would find; changes nothing. */
  async previewMailDomain(organizationId: number, name: string): Promise<MailDomainPreview> {
    const parsed = mailDomainPreviewSchema.safeParse(
      await request.post<unknown, MailDomainPreviewInput>(
        '/mail-domain-previews',
        { name },
        { headers: organizationHeaders(organizationId) }
      )
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Makes a verified domain the organization's default for new mailboxes. */
  async setDefaultMailDomain(organizationId: number, domainId: number): Promise<MailDomain> {
    validId(domainId);
    return parseMailDomain(
      await request.put<unknown>(`/mail-domains/${domainId}/default`, undefined, {
        headers: organizationHeaders(organizationId),
      }),
      domainId
    );
  },

  /** Turns a send-only domain into one that also receives; the setup runs in the background. */
  async enableReceiving(organizationId: number, domainId: number): Promise<MailDomain> {
    validId(domainId);
    return parseMailDomain(
      await request.post<unknown>(`/mail-domains/${domainId}/receiving`, undefined, {
        headers: organizationHeaders(organizationId),
      }),
      domainId
    );
  },

  async listMailZones(organizationId: number): Promise<MailZone[]> {
    const parsed = mailZoneListSchema.safeParse(
      await request.get<unknown>('/mail-zones', { headers: organizationHeaders(organizationId) })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async getMailZone(organizationId: number, zoneId: number): Promise<MailZone> {
    validId(zoneId);
    const parsed = mailZoneSchema.safeParse(
      await request.get<unknown>(`/mail-zones/${zoneId}`, {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success || parsed.data.id !== zoneId) throw invalidResponse();
    return parsed.data;
  },

  async deleteMailZone(organizationId: number, zoneId: number): Promise<void> {
    validId(zoneId);
    await request.delete<void>(`/mail-zones/${zoneId}`, {
      headers: organizationHeaders(organizationId),
    });
  },

  async listDnsRecords(organizationId: number, zoneId: number): Promise<MailDnsRecord[]> {
    validId(zoneId);
    const parsed = mailDnsRecordListSchema.safeParse(
      await request.get<unknown>(`/mail-zones/${zoneId}/dns-records`, {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async createDnsRecord(
    organizationId: number,
    zoneId: number,
    input: MailDnsRecordInput
  ): Promise<MailDnsRecord> {
    validId(zoneId);
    return parseDnsRecord(
      await request.post<unknown, MailDnsRecordInput>(`/mail-zones/${zoneId}/dns-records`, input, {
        headers: organizationHeaders(organizationId),
      })
    );
  },

  async updateDnsRecord(
    organizationId: number,
    zoneId: number,
    recordId: string,
    input: MailDnsRecordPatch
  ): Promise<MailDnsRecord> {
    validId(zoneId);
    validRecordId(recordId);
    return parseDnsRecord(
      await request.patch<unknown, MailDnsRecordPatch>(
        `/mail-zones/${zoneId}/dns-records/${recordId}`,
        input,
        { headers: organizationHeaders(organizationId) }
      ),
      recordId
    );
  },

  async deleteDnsRecord(organizationId: number, zoneId: number, recordId: string): Promise<void> {
    validId(zoneId);
    validRecordId(recordId);
    await request.delete<void>(`/mail-zones/${zoneId}/dns-records/${recordId}`, {
      headers: organizationHeaders(organizationId),
    });
  },
};

function parseMailDomain(value: unknown, expectedId?: number): MailDomain {
  const parsed = mailDomainSchema.safeParse(value);
  if (!parsed.success || (expectedId !== undefined && parsed.data.id !== expectedId)) {
    throw invalidResponse();
  }
  return parsed.data;
}

function parseDnsRecord(value: unknown, expectedId?: string): MailDnsRecord {
  const parsed = mailDnsRecordSchema.safeParse(value);
  if (!parsed.success || (expectedId !== undefined && parsed.data.id !== expectedId)) {
    throw invalidResponse();
  }
  return parsed.data;
}

assertContract<
  Accepts<z.input<typeof mailboxDomainListSchema>, ContractData<'listMailboxDomains', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailDomainListSchema>, ContractData<'listMailDomains', 200>>
>();
assertContract<Accepts<z.input<typeof mailDomainSchema>, ContractData<'connectMailDomain', 201>>>();
assertContract<Accepts<z.input<typeof mailDomainSchema>, ContractData<'setUpMailDomain', 200>>>();
assertContract<Accepts<z.input<typeof mailDomainSchema>, ContractData<'checkMailDomain', 200>>>();
assertContract<Sends<MailDomainInput, ContractRequest<'connectMailDomain'>>>();
assertContract<
  Accepts<z.input<typeof mailDomainSchema>, ContractData<'enableMailDomainReceiving', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailDomainPreviewSchema>, ContractData<'previewMailDomain', 200>>
>();
assertContract<Sends<MailDomainPreviewInput, ContractRequest<'previewMailDomain'>>>();
assertContract<Accepts<z.input<typeof mailZoneListSchema>, ContractData<'listMailZones', 200>>>();
assertContract<Accepts<z.input<typeof mailZoneSchema>, ContractData<'getMailZone', 200>>>();
assertContract<
  Accepts<z.input<typeof mailDnsRecordListSchema>, ContractData<'listMailDNSRecords', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailDnsRecordSchema>, ContractData<'createMailDNSRecord', 201>>
>();
assertContract<
  Accepts<z.input<typeof mailDnsRecordSchema>, ContractData<'updateMailDNSRecord', 200>>
>();
assertContract<Sends<MailDnsRecordInput, ContractRequest<'createMailDNSRecord'>>>();
assertContract<Sends<MailDnsRecordPatch, ContractRequest<'updateMailDNSRecord'>>>();
