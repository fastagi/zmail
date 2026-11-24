export type WorkspaceMode = 'personal' | 'work';

export type MailUiProviderId =
  | 'gmail'
  | 'outlook'
  | 'icloud'
  | 'qq'
  | 'netease163'
  | 'fastmail'
  | 'yahoo'
  | 'zoho'
  | 'exmail'
  | 'feishu'
  | 'aliyun'
  | 'sina'
  | 'custom';

export type MailUiAuthStrategy = 'oauth2' | 'app_password' | 'password';

export type MailUiTlsSecurity = 'tls' | 'starttls';

export type MailUiAccountKind = 'hosted' | 'external';

export type MailUiSyncStatus = 'idle' | 'syncing' | 'synced' | 'error' | 'paused';

export interface MailUiAccount {
  id: string;
  email: string;
  displayName?: string;
  providerId: MailUiProviderId | (string & {});
  kind: MailUiAccountKind;
  /** Optional explicit workspace override; defaults to 'work' for hosted and 'personal' for external. */
  mode?: WorkspaceMode;
  unreadCount?: number;
  syncStatus: MailUiSyncStatus;
  lastSyncedAt?: string | null;
  lastError?: string | null;
  canSend?: boolean;
}

export interface MailUiMessage {
  id: string;
  threadId: string;
  accountId: string;
  accountEmail?: string;
  direction?: 'inbound' | 'outbound';
  from: string;
  fromName?: string;
  to: readonly string[];
  cc?: readonly string[];
  bcc?: readonly string[];
  subject: string;
  snippet?: string;
  bodyText?: string;
  bodyHtml?: string;
  receivedAt: string;
  read: boolean;
  starred: boolean;
  archived?: boolean;
  folder?: string;
  labels?: readonly string[];
  attachmentCount?: number;
}

export interface MailUiThread {
  id: string;
  accountId: string;
  subject: string;
  participants: readonly string[];
  messageIds: readonly string[];
  messageCount: number;
  unread: boolean;
  starred: boolean;
  archived?: boolean;
  lastMessageAt: string;
  snippet?: string;
}

export interface MailUiProviderPreset {
  id: MailUiProviderId;
  label: string;
  shortLabel: string;
  region: 'global' | 'cn';
  enterprise: boolean;
  authStrategy: MailUiAuthStrategy;
  imapHost: string;
  imapPort: number;
  imapTls: MailUiTlsSecurity;
  smtpHost: string;
  smtpPort: number;
  smtpTls: MailUiTlsSecurity;
  domains: readonly string[];
  helpUrl: string;
  badgeColor: string;
  guidance: string;
}

export interface MailUiProviderDetection {
  email: string;
  domain: string;
  valid: boolean;
  matched: boolean;
  preset: MailUiProviderPreset;
}

export type MailUiComposeMode = 'new' | 'reply' | 'reply_all' | 'forward';

export interface MailUiComposePayload {
  mode: MailUiComposeMode;
  fromAccountId: string;
  fromEmail: string;
  to: string[];
  cc?: string[];
  bcc?: string[];
  subject: string;
  bodyText: string;
  bodyHtml?: string;
  inReplyToMessageId?: string;
  threadId?: string;
}
