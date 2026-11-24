export type {
  MailUiAccount,
  MailUiAccountKind,
  MailUiAuthStrategy,
  MailUiComposeMode,
  MailUiComposePayload,
  MailUiMessage,
  MailUiProviderDetection,
  MailUiProviderId,
  MailUiProviderPreset,
  MailUiSyncStatus,
  MailUiThread,
  MailUiTlsSecurity,
  WorkspaceMode,
} from './types.ts';

export {
  detectProviderFromEmail,
  getProviderPreset,
  MAIL_UI_MAX_CONNECTED_ACCOUNTS,
  MAIL_UI_PROVIDER_PRESETS,
} from './providers.ts';

export {
  buildReplyPayload,
  computeTotalUnread,
  computeUnreadByAccount,
  filterAccountsForMode,
  filterMessagesForMode,
  resolveAccountMode,
  resolveReplySenderAccount,
  type FilterMessagesOptions,
} from './selectors.ts';

export { MailShell, type MailShellProps } from './components/MailShell.tsx';

export {
  createAccountRailController,
  createInboxListController,
  createMailUiAgent,
  type AccountRailController,
  type AccountRailControllerOptions,
  type AccountRailState,
  type InboxListController,
  type InboxListControllerOptions,
  type InboxListState,
  type KeyEventLike,
  type MailUiAgent,
  type MailUiAgentActResult,
  type MailUiAgentIntent,
  type MailUiAgentOptions,
  type MailUiAgentSnapshot,
  type VirtualWindow,
} from './primitives/inbox-behaviors.ts';
