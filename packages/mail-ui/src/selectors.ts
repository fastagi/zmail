import { MAIL_UI_MAX_CONNECTED_ACCOUNTS } from './providers.ts';
import type {
  MailUiAccount,
  MailUiComposeMode,
  MailUiComposePayload,
  MailUiMessage,
  WorkspaceMode,
} from './types.ts';

export interface FilterMessagesOptions {
  selectedAccountId?: string | null;
  searchQuery?: string;
  includeArchived?: boolean;
}

function normalizeEmail(value: string | undefined | null): string {
  if (!value) {
    return '';
  }
  const trimmed = value.trim();
  const angleMatch = /<([^>]+)>/u.exec(trimmed);
  return (angleMatch?.[1] ?? trimmed).trim().toLowerCase();
}

export function resolveAccountMode(account: MailUiAccount): WorkspaceMode {
  if (account.mode) {
    return account.mode;
  }
  return account.kind === 'external' ? 'personal' : 'work';
}

export function filterAccountsForMode(
  accounts: readonly MailUiAccount[],
  mode: WorkspaceMode,
  maxAccounts: number = MAIL_UI_MAX_CONNECTED_ACCOUNTS
): MailUiAccount[] {
  const limit = Math.max(0, maxAccounts);
  return accounts
    .filter((account) => resolveAccountMode(account) === mode)
    .slice(0, limit);
}

/**
 * Filters messages to those belonging to accounts in the active WorkspaceMode,
 * optionally narrowed to a single account and search query, sorted newest-first.
 */
export function filterMessagesForMode(
  messages: readonly MailUiMessage[],
  accounts: readonly MailUiAccount[],
  mode: WorkspaceMode,
  options: FilterMessagesOptions = {}
): MailUiMessage[] {
  const { selectedAccountId = null, searchQuery = '', includeArchived = false } = options;
  const visibleAccountIds = new Set(
    filterAccountsForMode(accounts, mode, Number.POSITIVE_INFINITY).map((account) => account.id)
  );
  const normalizedQuery = searchQuery.trim().toLowerCase();
  const filterAccountId =
    selectedAccountId && selectedAccountId !== 'all' ? selectedAccountId : null;

  return messages
    .filter((message) => {
      if (!visibleAccountIds.has(message.accountId)) {
        return false;
      }
      if (filterAccountId && message.accountId !== filterAccountId) {
        return false;
      }
      if (!includeArchived && message.archived) {
        return false;
      }
      if (normalizedQuery.length > 0) {
        const haystack = [
          message.subject,
          message.from,
          message.fromName ?? '',
          message.snippet ?? '',
          message.bodyText ?? '',
          ...message.to,
          ...(message.cc ?? []),
        ]
          .join(' ')
          .toLowerCase();
        if (!haystack.includes(normalizedQuery)) {
          return false;
        }
      }
      return true;
    })
    .sort((a, b) => Date.parse(b.receivedAt) - Date.parse(a.receivedAt));
}

/**
 * Computes unread message counts per accountId (excluding archived messages).
 * Initializes all provided accounts to 0 so UI badges can safely read every key.
 */
export function computeUnreadByAccount(
  messages: readonly MailUiMessage[],
  accounts: readonly MailUiAccount[] = []
): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const account of accounts) {
    counts[account.id] = 0;
  }
  for (const message of messages) {
    if (message.read || message.archived) {
      continue;
    }
    counts[message.accountId] = (counts[message.accountId] ?? 0) + 1;
  }
  return counts;
}

export function computeTotalUnread(
  unreadByAccount: Readonly<Record<string, number>>,
  accounts?: readonly MailUiAccount[]
): number {
  if (accounts) {
    return accounts.reduce(
      (total, account) => total + (unreadByAccount[account.id] ?? account.unreadCount ?? 0),
      0
    );
  }
  return Object.values(unreadByAccount).reduce((total, count) => total + count, 0);
}

/**
 * Resolves which connected account should send a reply/forward so replying
 * always uses the exact account that received the message.
 */
export function resolveReplySenderAccount(
  message: MailUiMessage,
  accounts: readonly MailUiAccount[]
): MailUiAccount | null {
  if (accounts.length === 0) {
    return null;
  }

  const byId = accounts.find((account) => account.id === message.accountId);
  if (byId) {
    return byId;
  }

  const targetAccountEmail = normalizeEmail(message.accountEmail);
  if (targetAccountEmail) {
    const byAccountEmail = accounts.find(
      (account) => normalizeEmail(account.email) === targetAccountEmail
    );
    if (byAccountEmail) {
      return byAccountEmail;
    }
  }

  const recipientAddresses = new Set(
    [...message.to, ...(message.cc ?? [])].map((addr) => normalizeEmail(addr)).filter(Boolean)
  );
  if (recipientAddresses.size > 0) {
    const byRecipient = accounts.find((account) =>
      recipientAddresses.has(normalizeEmail(account.email))
    );
    if (byRecipient) {
      return byRecipient;
    }
  }

  return accounts.find((account) => account.canSend !== false) ?? accounts[0] ?? null;
}

function prefixSubject(subject: string, prefix: 'Re:' | 'Fwd:'): string {
  const trimmed = subject.trim();
  if (!trimmed) {
    return prefix;
  }
  const pattern = prefix === 'Re:' ? /^re:\s*/iu : /^fwd:\s*/iu;
  return pattern.test(trimmed) ? trimmed : `${prefix} ${trimmed}`;
}

export function buildReplyPayload(
  message: MailUiMessage,
  accounts: readonly MailUiAccount[],
  mode: Exclude<MailUiComposeMode, 'new'> = 'reply'
): MailUiComposePayload | null {
  const sender = resolveReplySenderAccount(message, accounts);
  if (!sender) {
    return null;
  }

  const senderEmailNorm = normalizeEmail(sender.email);
  if (mode === 'forward') {
    return {
      mode: 'forward',
      fromAccountId: sender.id,
      fromEmail: sender.email,
      to: [],
      subject: prefixSubject(message.subject, 'Fwd:'),
      bodyText: message.bodyText ? `\n\n--- Forwarded message ---\n${message.bodyText}` : '',
      inReplyToMessageId: message.id,
      threadId: message.threadId,
    };
  }

  const replyTo = message.from ? [message.from] : [];
  const cc =
    mode === 'reply_all'
      ? [...message.to, ...(message.cc ?? [])].filter((addr) => {
          const norm = normalizeEmail(addr);
          return norm.length > 0 && norm !== senderEmailNorm && norm !== normalizeEmail(message.from);
        })
      : undefined;

  return {
    mode,
    fromAccountId: sender.id,
    fromEmail: sender.email,
    to: replyTo,
    ...(cc && cc.length > 0 ? { cc } : {}),
    subject: prefixSubject(message.subject, 'Re:'),
    bodyText: '',
    inReplyToMessageId: message.id,
    threadId: message.threadId,
  };
}
