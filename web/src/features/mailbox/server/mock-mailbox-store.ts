import 'server-only';

import type {
  CreateMailboxInput,
  InboxCounts,
  InboxPage,
  MailDayStats,
  MailFolder,
  MailLabel,
  MailLogFilters,
  MailLogPage,
  MailMessage,
  MailMessageCounts,
  MailMessageFilters,
  MailMessagePage,
  MailMessageUpdatesResult,
  MailRole,
  MailTemplateInput,
  MailTemplateKind,
  MailTemplateSummary,
  MailTrashPurge,
  Mailbox,
  MailboxAccessEventPage,
  MailboxDomain,
  MailboxImportResult,
  MailboxListScope,
  MailboxMember,
  MailboxSealedDeletion,
  MailboxType,
  MemberImportInput,
  MemberImportResult,
  SendMailMessageInput,
} from '@/features/mailbox/types';
import type { MockWelcomeCandidate } from '@/features/mailbox/server/mock-member-welcome-store';
import { mockOrganizationStore } from '@/features/organization/server/mock-organization-store';
import {
  assignMailboxRole,
  createMailRole,
  deleteMailRole,
  deleteMailboxSealedHistory,
  handOverMailbox,
  listMailRoles,
  listMailboxAccessEvents,
  listMailboxMembers,
  removeMailboxMember,
  setMailboxMember,
  setMailboxWebhooks,
  updateMailRole,
} from '@/features/mailbox/server/mock-store/access-store';
import {
  getMailStats,
  getUnifiedInboxCounts,
  listMailLog,
  listMailboxMessages,
  listUnifiedInbox,
} from '@/features/mailbox/server/mock-store/inbox-store';
import {
  buildLedgerDirectory,
  countMailboxesOnDomain,
  createExternalMailbox,
  createMailbox,
  deleteMailbox,
  domainInUse,
  exportMailboxesCsv,
  hasMailbox,
  importMailboxRows,
  listDomains,
  listMailboxDirectory,
  listMailboxes,
  removeExternalMailbox,
  renameMailbox,
  setConnectedMailboxSend,
  syncExternalMailbox,
} from '@/features/mailbox/server/mock-store/mailbox-admin-store';
import {
  deleteMailboxFolder,
  deleteMailboxLabel,
  deleteMailboxMessage,
  getMailboxAttachment,
  getMailboxCounts,
  getMailboxMessage,
  getMailboxThread,
  listMailboxFolders,
  listMailboxLabels,
  purgeMailboxTrash,
  saveMailboxFolder,
  saveMailboxLabel,
  sendMailboxMessage,
  updateMailboxMessage,
  updateMailboxMessages,
} from '@/features/mailbox/server/mock-store/message-store';
import {
  claimReservedMailboxes,
  importRosterMembers,
  listWelcomeCandidates,
} from '@/features/mailbox/server/mock-store/roster-store';
import { MockMailboxState } from '@/features/mailbox/server/mock-store/state';
import {
  createMailTemplate,
  deleteMailTemplate,
  getMailTemplate,
  listMailTemplates,
  sendMailTemplateTest,
  updateMailTemplate,
} from '@/features/mailbox/server/mock-store/template-store';
import {
  MOCK_DAILY_SEND_LIMIT,
  MOCK_MAILBOX_DOMAIN,
  type MailboxViewer,
  type MockAccessError,
  type MockAccessResult,
  type MockDirectoryPage,
  type MockDirectoryQuery,
  type MockImportDomainResolver,
  type MockInboxQuery,
  type MockLedgerDirectory,
  type MockMailAttachmentResult,
  type MockMailDeleteResult,
  type MockMailMessagePageResult,
  type MockMailMessageResult,
  type MockMailRoleInput,
  type MockMailSendResult,
  type MockMailTemplateResult,
  type MockMailboxCreateResult,
  type MockMailboxStoreDependencies,
  type MockMessageChange,
  type MockOrganizeError,
  type MockOrganizeResult,
  type MockRosterInviter,
} from '@/features/mailbox/server/mock-store/types';

export { MOCK_DAILY_SEND_LIMIT, MOCK_MAILBOX_DOMAIN };
export { mailboxAccessFor } from '@/features/mailbox/server/mock-store/helpers';
export type {
  MailboxViewer,
  MockAccessError,
  MockAccessResult,
  MockDirectoryPage,
  MockDirectoryQuery,
  MockImportDomainResolver,
  MockInboxQuery,
  MockLedgerDirectory,
  MockMailAttachmentResult,
  MockMailDeleteResult,
  MockMailMessagePageResult,
  MockMailMessageResult,
  MockMailRoleInput,
  MockMailSendResult,
  MockMailTemplateResult,
  MockMailboxCreateResult,
  MockMessageChange,
  MockOrganizeError,
  MockOrganizeResult,
  MockRosterInviter,
};

/**
 * Development-only mailboxes, isolated per organization. It mirrors the browser-facing rules of
 * the Go mailbox starter: address validation and reserved names, addresses that stay reserved
 * after a mailbox is deleted, the per-organization mailbox limit, send validation, newest-first
 * listings without bodies, the cross-mailbox log, and per-day counts. Access follows
 * `contracts/MAILBOXES.md`: personal and shared mailboxes, member permissions, 404 without any
 * access and 403 without the needed one, per-person read state, handover with a seal boundary,
 * the webhook switch, and the access history. Mail is organized as the API does it: placements
 * shared by everyone, per-person stars, threads linked by `in_reply_to`, folders and labels with
 * their limits, a trash that keeps mail for 30 days, and a case-insensitive substring search that
 * answers a one-character text as too broad. Organization membership comes from the
 * organization mock; a departure is noticed on the next access to the mailbox. Nothing leaves the
 * process: a "sent" message is only stored, which is what the `log` provider means.
 */
export class MockMailboxStore {
  private readonly state: MockMailboxState;

  constructor(dependencies: Partial<MockMailboxStoreDependencies> = {}) {
    this.state = new MockMailboxState(dependencies);
  }

  private get organizations() {
    return this.state.organizations;
  }

  domains(
    verifiedDomains: readonly string[] = [],
    defaultDomain: string | null = null
  ): MailboxDomain[] {
    return listDomains(verifiedDomains, defaultDomain);
  }

  countOnDomain(organizationId: number, name: string): number {
    return countMailboxesOnDomain(this.state, organizationId, name);
  }

  usesDomain(name: string): boolean {
    return domainInUse(this.state, name);
  }

  list(organizationId: number, viewer: MailboxViewer, scope?: MailboxListScope): Mailbox[] {
    return listMailboxes(this.state, organizationId, viewer, scope);
  }

  create(
    organizationId: number,
    viewer: MailboxViewer,
    input: Omit<CreateMailboxInput, 'type'> & { type?: unknown },
    addressDomain: string = MOCK_MAILBOX_DOMAIN
  ): MockMailboxCreateResult {
    return createMailbox(this.state, organizationId, viewer, input, addressDomain);
  }

  createExternal(
    organizationId: number,
    ownerUserId: number,
    address: string,
    provider: string,
    backfillDays: number
  ): number {
    return createExternalMailbox(
      this.state,
      organizationId,
      ownerUserId,
      address,
      provider,
      backfillDays
    );
  }

  setConnectedSend(organizationId: number, mailboxId: number, sends: boolean): void {
    setConnectedMailboxSend(this.state, organizationId, mailboxId, sends);
  }

  syncExternal(
    organizationId: number,
    mailboxId: number,
    provider: string,
    backfillDays: number
  ): void {
    syncExternalMailbox(this.state, organizationId, mailboxId, provider, backfillDays);
  }

  removeExternal(organizationId: number, mailboxId: number): number | null {
    return removeExternalMailbox(this.state, organizationId, mailboxId);
  }

  directory(
    organizationId: number,
    viewer: MailboxViewer,
    query: MockDirectoryQuery
  ): { ok: true; value: MockDirectoryPage } | { ok: false; error: 'forbidden' } {
    return listMailboxDirectory(this.state, organizationId, viewer, query);
  }

  rename(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    rawDisplayName: string
  ):
    | { ok: true; mailbox: Mailbox }
    | { ok: false; error: 'forbidden' | 'mailbox_not_found' | 'invalid' } {
    return renameMailbox(this.state, organizationId, mailboxId, viewer, rawDisplayName);
  }

  importRows(
    organizationId: number,
    viewer: MailboxViewer,
    rows: readonly { address: string; display_name: string; members: string }[],
    dryRun: boolean,
    resolveDomain: MockImportDomainResolver
  ): { ok: true; value: MailboxImportResult } | { ok: false; error: 'forbidden' } {
    return importMailboxRows(this.state, organizationId, viewer, rows, dryRun, resolveDomain);
  }

  importMembers(
    organizationId: number,
    viewer: MailboxViewer,
    input: MemberImportInput,
    resolveDomain: MockImportDomainResolver
  ): { ok: true; value: MemberImportResult } | { ok: false; error: 'forbidden' } {
    return importRosterMembers(this.state, organizationId, viewer, input, resolveDomain);
  }

  welcomeCandidates(organizationId: number, viewerUserId: number): MockWelcomeCandidate[] {
    return listWelcomeCandidates(this.state, organizationId, viewerUserId);
  }

  claimReserved(organizationId: number, userId: number, email: string): number[] {
    return claimReservedMailboxes(this.state, organizationId, userId, email);
  }

  exportCsv(
    organizationId: number,
    viewer: MailboxViewer,
    type: MailboxType
  ): { ok: true; csv: string; filename: string } | { ok: false; error: 'forbidden' } {
    return exportMailboxesCsv(this.state, organizationId, viewer, type);
  }

  delete(organizationId: number, mailboxId: number, viewer: MailboxViewer): MockMailDeleteResult {
    return deleteMailbox(this.state, organizationId, mailboxId, viewer);
  }

  listMessages(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    filters: MailMessageFilters,
    page: number,
    perPage: number
  ): MockOrganizeResult<MailMessagePage> {
    return listMailboxMessages(this.state, organizationId, mailboxId, viewer, filters, page, perPage);
  }

  listLog(
    organizationId: number,
    viewer: MailboxViewer,
    filters: MailLogFilters,
    page: number,
    perPage: number
  ): MailLogPage {
    return listMailLog(this.state, organizationId, viewer, filters, page, perPage);
  }

  listInbox(
    organizationId: number,
    viewer: MailboxViewer,
    query: MockInboxQuery
  ): MockOrganizeResult<InboxPage> {
    return listUnifiedInbox(this.state, organizationId, viewer, query);
  }

  inboxCounts(organizationId: number, viewer: MailboxViewer, tz: string): InboxCounts {
    return getUnifiedInboxCounts(this.state, organizationId, viewer, tz);
  }

  stats(organizationId: number, viewer: MailboxViewer, days: number): MailDayStats[] {
    return getMailStats(this.state, organizationId, viewer, days);
  }

  getMessage(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    viewer: MailboxViewer
  ): MockMailMessageResult {
    return getMailboxMessage(this.state, organizationId, mailboxId, messageId, viewer);
  }

  updateMessage(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    viewer: MailboxViewer,
    change: MockMessageChange
  ): MockOrganizeResult<MailMessage> {
    return updateMailboxMessage(this.state, organizationId, mailboxId, messageId, viewer, change);
  }

  updateMessages(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    messageIds: number[],
    change: MockMessageChange
  ): MockOrganizeResult<MailMessageUpdatesResult> {
    return updateMailboxMessages(this.state, organizationId, mailboxId, viewer, messageIds, change);
  }

  deleteMessage(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    viewer: MailboxViewer
  ): MockMailDeleteResult {
    return deleteMailboxMessage(this.state, organizationId, mailboxId, messageId, viewer);
  }

  purgeTrash(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer
  ): MockOrganizeResult<MailTrashPurge> {
    return purgeMailboxTrash(this.state, organizationId, mailboxId, viewer);
  }

  thread(
    organizationId: number,
    mailboxId: number,
    threadId: number,
    viewer: MailboxViewer
  ): MockOrganizeResult<MailMessage[]> {
    return getMailboxThread(this.state, organizationId, mailboxId, threadId, viewer);
  }

  counts(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer
  ): MockOrganizeResult<MailMessageCounts> {
    return getMailboxCounts(this.state, organizationId, mailboxId, viewer);
  }

  folders(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer
  ): MockOrganizeResult<MailFolder[]> {
    return listMailboxFolders(this.state, organizationId, mailboxId, viewer);
  }

  saveFolder(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    rawName: string,
    folderId?: number
  ): MockOrganizeResult<MailFolder> {
    return saveMailboxFolder(this.state, organizationId, mailboxId, viewer, rawName, folderId);
  }

  deleteFolder(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    folderId: number
  ): MockOrganizeResult<null> {
    return deleteMailboxFolder(this.state, organizationId, mailboxId, viewer, folderId);
  }

  labels(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer
  ): MockOrganizeResult<MailLabel[]> {
    return listMailboxLabels(this.state, organizationId, mailboxId, viewer);
  }

  saveLabel(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    input: { name: string; color: string },
    labelId?: number
  ): MockOrganizeResult<MailLabel> {
    return saveMailboxLabel(this.state, organizationId, mailboxId, viewer, input, labelId);
  }

  deleteLabel(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    labelId: number
  ): MockOrganizeResult<null> {
    return deleteMailboxLabel(this.state, organizationId, mailboxId, viewer, labelId);
  }

  listRoles(organizationId: number, viewer: MailboxViewer): MailRole[] {
    return listMailRoles(this.state, organizationId, viewer);
  }

  createRole(
    organizationId: number,
    viewer: MailboxViewer,
    input: MockMailRoleInput
  ): MockOrganizeResult<MailRole> {
    return createMailRole(this.state, organizationId, viewer, input);
  }

  updateRole(
    organizationId: number,
    viewer: MailboxViewer,
    roleId: number,
    input: MockMailRoleInput
  ): MockOrganizeResult<MailRole> {
    return updateMailRole(this.state, organizationId, viewer, roleId, input);
  }

  deleteRole(
    organizationId: number,
    viewer: MailboxViewer,
    roleId: number
  ): MockOrganizeResult<null> {
    return deleteMailRole(this.state, organizationId, viewer, roleId);
  }

  assignRole(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    roleId: number | null
  ): MockOrganizeResult<Mailbox> {
    return assignMailboxRole(this.state, organizationId, mailboxId, viewer, roleId);
  }

  attachment(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    attachmentId: number,
    viewer: MailboxViewer
  ): MockMailAttachmentResult {
    return getMailboxAttachment(this.state, organizationId, mailboxId, messageId, attachmentId, viewer);
  }

  members(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer
  ): MockAccessResult<MailboxMember[]> {
    return listMailboxMembers(this.state, organizationId, mailboxId, viewer);
  }

  setMember(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    userId: number,
    permission: unknown
  ): MockAccessResult<MailboxMember> {
    return setMailboxMember(this.state, organizationId, mailboxId, viewer, userId, permission);
  }

  removeMember(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    userId: number
  ): MockAccessResult<null> {
    return removeMailboxMember(this.state, organizationId, mailboxId, viewer, userId);
  }

  accessEvents(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    page: number,
    perPage: number
  ): MockAccessResult<MailboxAccessEventPage> {
    return listMailboxAccessEvents(this.state, organizationId, mailboxId, viewer, page, perPage);
  }

  handOver(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    successorUserId: number
  ): MockAccessResult<Mailbox> {
    return handOverMailbox(this.state, organizationId, mailboxId, viewer, successorUserId);
  }

  deleteSealedHistory(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    confirmAddress: string
  ): MockAccessResult<MailboxSealedDeletion> {
    return deleteMailboxSealedHistory(
      this.state,
      organizationId,
      mailboxId,
      viewer,
      confirmAddress
    );
  }

  setWebhooks(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    enabled: boolean
  ): MockAccessResult<Mailbox> {
    return setMailboxWebhooks(this.state, organizationId, mailboxId, viewer, enabled);
  }

  listTemplates(organizationId: number, kind?: MailTemplateKind): MailTemplateSummary[] {
    return listMailTemplates(this.state, organizationId, kind);
  }

  hasMailbox(organizationId: number, mailboxId: number): boolean {
    return hasMailbox(this.state, organizationId, mailboxId);
  }

  getTemplate(organizationId: number, templateId: number): MockMailTemplateResult {
    return getMailTemplate(this.state, organizationId, templateId);
  }

  createTemplate(organizationId: number, input: MailTemplateInput): MockMailTemplateResult {
    return createMailTemplate(this.state, organizationId, input);
  }

  updateTemplate(
    organizationId: number,
    templateId: number,
    input: MailTemplateInput
  ): MockMailTemplateResult {
    return updateMailTemplate(this.state, organizationId, templateId, input);
  }

  deleteTemplate(organizationId: number, templateId: number): boolean {
    return deleteMailTemplate(this.state, organizationId, templateId);
  }

  sendTemplateTest(
    organizationId: number,
    templateId: number,
    mailboxId: number,
    to: readonly string[],
    variables: Readonly<Record<string, string>>,
    viewer: MailboxViewer
  ): MockMailSendResult {
    return sendMailTemplateTest(
      this.state,
      organizationId,
      templateId,
      mailboxId,
      to,
      variables,
      viewer
    );
  }

  send(
    organizationId: number,
    mailboxId: number,
    input: SendMailMessageInput,
    viewer?: MailboxViewer
  ): MockMailSendResult {
    return sendMailboxMessage(this.state, organizationId, mailboxId, input, viewer);
  }

  ledgerDirectory(organizationId: number, viewer: MailboxViewer): MockLedgerDirectory {
    return buildLedgerDirectory(this.state, organizationId, viewer);
  }

  reset(): void {
    this.state.reset();
  }
}

export const mockMailboxStore = new MockMailboxStore();
mockOrganizationStore.onMemberJoined((organizationId, userId, email) => {
  mockMailboxStore.claimReserved(organizationId, userId, email);
});
