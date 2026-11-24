import 'server-only';

/**
 * Browser route boundary barrel for the mailbox feature. Re-exports all subdomain route
 * handlers and shared helpers so App Router endpoints keep their existing import path.
 */

export {
  resolveMailboxRoute,
  privateMailboxResponse,
} from './route-shared';
export {
  listMailboxDomainsRoute,
  listMailDomainsRoute,
  getMailDomainRoute,
  connectMailDomainRoute,
  refreshMailDomainRoute,
  enableMailDomainReceivingRoute,
  setDefaultMailDomainRoute,
  previewMailDomainRoute,
  deleteMailDomainRoute,
  listMailZonesRoute,
  getMailZoneRoute,
  deleteMailZoneRoute,
  listMailDnsRecordsRoute,
  createMailDnsRecordRoute,
  updateMailDnsRecordRoute,
  deleteMailDnsRecordRoute,
  getCloudflareConnectionRoute,
  changeCloudflareConnectionRoute,
} from './domain-route';
export {
  listMailboxesRoute,
  listMailboxDirectoryRoute,
  updateMailboxRoute,
  importMailboxesRoute,
  importMembersRoute,
  exportMailboxesRoute,
  createMailboxRoute,
  deleteMailboxRoute,
} from './roster-route';
export {
  listMailMessagesRoute,
  listMailLogRoute,
  listInboxMessagesRoute,
  getInboxMessageCountsRoute,
  listMailMessageStatsRoute,
  sendMailMessageRoute,
} from './inbox-route';
export {
  getMailMessageRoute,
  downloadMailAttachmentRoute,
  updateMailMessageRoute,
  updateMailMessagesRoute,
  revealMailMessageRoute,
  purgeMailTrashRoute,
  getMailThreadRoute,
  getMailMessageCountsRoute,
  listMailFoldersRoute,
  createMailFolderRoute,
  renameMailFolderRoute,
  deleteMailFolderRoute,
  listMailLabelsRoute,
  createMailLabelRoute,
  updateMailLabelRoute,
  deleteMailLabelRoute,
  deleteMailMessageRoute,
} from './message-route';
export {
  listMailBroadcastsRoute,
  getMailBroadcastRoute,
  saveMailBroadcastRoute,
  deleteMailBroadcastRoute,
  sendMailBroadcastRoute,
  controlMailBroadcastRoute,
  listMailBroadcastRecipientsRoute,
} from './broadcast-route';
export {
  listMailContactsRoute,
  createMailContactRoute,
  updateMailContactRoute,
  deleteMailContactRoute,
  importMailContactsRoute,
  listMailContactListsRoute,
  saveMailContactListRoute,
  deleteMailContactListRoute,
} from './contact-route';
export {
  listMailTemplatesRoute,
  createMailTemplateRoute,
  getMailTemplateRoute,
  updateMailTemplateRoute,
  deleteMailTemplateRoute,
  sendMailTemplateTestRoute,
} from './template-route';
export {
  listMailboxMembersRoute,
  setMailboxMemberRoute,
  removeMailboxMemberRoute,
  listMailboxAccessEventsRoute,
  handOverMailboxRoute,
  deleteMailboxSealedMessagesRoute,
  setMailboxWebhookSettingRoute,
  listMailRolesRoute,
  createMailRoleRoute,
  updateMailRoleRoute,
  deleteMailRoleRoute,
  setMailboxRoleRoute,
} from './access-route';
export {
  listMailAgentGrantsRoute,
  issueMailAgentGrantRoute,
  revokeMailAgentGrantRoute,
  listMailAgentGrantActivityRoute,
  listMailAgentDraftsRoute,
} from './agent-grant-route';
export {
  listPlatformAccountsRoute,
  createPlatformAccountRoute,
  updatePlatformAccountRoute,
  deletePlatformAccountRoute,
  listPlatformAccountEventsRoute,
  handOverPlatformAccountsRoute,
} from './platform-account-route';
export {
  getMailTriageSettingRoute,
  updateMailTriageSettingRoute,
  setMailboxTriageSettingRoute,
  updateMailMessageTriageRoute,
  createMailReplyDraftRoute,
} from './triage-route';
export {
  listExternalMailConnectorsRoute,
  listExternalMailConnectionsRoute,
  startExternalMailAuthorizationRoute,
  completeExternalMailAuthorizationRoute,
  revokeExternalMailConnectionRoute,
  bindExternalMailCredentialsRoute,
  listExternalMailSyncRunsRoute,
  purgeExternalMailCopiesRoute,
  listExternalMailConnectionDirectoryRoute,
  forceDisconnectExternalMailConnectionRoute,
} from './external-mail-route';
export {
  getMailSignatureRoute,
  updateMailSignatureRoute,
  listMailAppPasswordsRoute,
  createMailAppPasswordRoute,
  revokeMailAppPasswordRoute,
  getMailClientSettingsRoute,
  updateMailClientSettingsRoute,
} from './client-access-route';
export {
  getMemberWelcomeSettingRoute,
  updateMemberWelcomeSettingRoute,
  previewMemberWelcomeRoute,
  listMemberWelcomeMailsRoute,
  resendMemberWelcomeMailRoute,
} from './member-welcome-route';
export {
  getMailThreadAssignmentRoute,
  updateMailThreadAssignmentRoute,
  listMailThreadNotesRoute,
  createMailThreadNoteRoute,
  deleteMailThreadNoteRoute,
  listMailDraftsRoute,
  showMailDraftRoute,
  createMailDraftRoute,
  updateMailDraftRoute,
  deleteMailDraftRoute,
} from './collaboration-route';
