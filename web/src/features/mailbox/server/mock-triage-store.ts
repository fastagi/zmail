import 'server-only';

import type {
  MailReplyDraft,
  MailTriageOverrideCategory,
  MailTriageSetting,
  MailTriageSettingInput,
} from '@/features/mailbox/triage-schemas';
import type {
  InboxItem,
  InboxTriageCounts,
  Mailbox,
  MailMessage,
  MailTriage,
  MailTriageCategory,
} from '@/features/mailbox/types';
import type { MailboxViewer } from '@/features/mailbox/server/mock-mailbox-store';

/**
 * Development mock of AI triage (`contracts/MAILBOXES.md`, AI Triage). It stands in for the API's
 * `fake` provider: the instance always has a model, the organization and mailbox switches start
 * off, and a result is computed when read from the subject and snippet with the fake rules instead
 * of by a worker, so it also covers mail received before the switches were turned on. A reader's
 * own decision is kept and wins over the rules. Mailbox and message access come from the mailbox
 * mock, which the route consults first.
 */

const instanceDailyCap = 500;
const defaultDailyCap = 500;
// Built from strings so the Chinese stays escaped: the full-width question mark, "can you", and
// "<month>M<day>D before".
const questionPattern = new RegExp('[?\\uff1f]|\\bcan you\\b|\\u80fd\\u4e0d\\u80fd', 'iu');
const deadlinePattern = new RegExp(
  '\\b(?:before|by)\\s+(?:\\d{4}-\\d{2}-\\d{2}|monday|tuesday|wednesday|thursday|friday|saturday|sunday|tomorrow)\\b' +
    '|\\d{1,2}\\u6708\\d{1,2}[\\u65e5\\u53f7]\\u524d',
  'iu'
);

interface StoredSetting {
  enabled: boolean;
  dailyMessageCap: number;
  draftEnabled: boolean;
  updatedAt: string | null;
}

interface StoredOverride {
  category: MailTriageOverrideCategory;
  dueAt: string | null;
}

export type MockTriageError = 'unavailable' | 'forbidden';

/** The fake provider's rules: a question asks for a reply, a date for a deadline, else FYI. */
export function fakeTriage(text: string): MailTriage {
  if (deadlinePattern.test(text)) {
    return { category: 'deadline', reason: 'Names a date to act by', due_at: null, source: 'ai' };
  }
  if (questionPattern.test(text)) {
    return { category: 'reply', reason: 'Asks you a question', due_at: null, source: 'ai' };
  }
  return { category: 'fyi', reason: 'For your information', due_at: null, source: 'ai' };
}

/** Only owners and admins write the organization's switches. */
function managesOrganization(viewer: MailboxViewer): boolean {
  return viewer.role === 'owner' || viewer.role === 'admin';
}

/**
 * A personal mailbox's switch belongs to its owner while they are a member; a shared mailbox's to
 * those who manage its members.
 */
export function maySwitchMailboxTriage(mailbox: Mailbox, viewer: MailboxViewer): boolean {
  if (mailbox.type === 'personal') {
    return mailbox.owner_user_id === viewer.userId && !mailbox.awaiting_handover;
  }
  return mailbox.access.manage_members;
}

export class MockTriageStore {
  private settings = new Map<number, StoredSetting>();
  private mailboxes = new Map<number, Set<number>>();
  private overrides = new Map<string, StoredOverride>();

  constructor(private readonly now: () => Date = () => new Date()) {}

  setting(organizationId: number): MailTriageSetting {
    const stored = this.settings.get(organizationId);
    return {
      available: true,
      enabled: stored?.enabled ?? false,
      daily_message_cap: stored?.dailyMessageCap ?? defaultDailyCap,
      draft_enabled: stored?.draftEnabled ?? false,
      instance_daily_cap: instanceDailyCap,
      updated_at: stored?.updatedAt ?? null,
    };
  }

  saveSetting(
    organizationId: number,
    viewer: MailboxViewer,
    input: MailTriageSettingInput
  ): { ok: true; value: MailTriageSetting } | { ok: false; error: MockTriageError } {
    if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
    this.settings.set(organizationId, {
      enabled: input.enabled,
      dailyMessageCap: input.daily_message_cap,
      draftEnabled: input.draft_enabled,
      updatedAt: this.now()
        .toISOString()
        .replace(/\.\d{3}Z$/u, 'Z'),
    });
    return { ok: true, value: this.setting(organizationId) };
  }

  enabled(organizationId: number): boolean {
    return this.settings.get(organizationId)?.enabled ?? false;
  }

  draftEnabled(organizationId: number): boolean {
    const stored = this.settings.get(organizationId);
    return (stored?.enabled ?? false) && (stored?.draftEnabled ?? false);
  }

  mailboxEnabled(organizationId: number, mailboxId: number): boolean {
    return this.mailboxes.get(organizationId)?.has(mailboxId) ?? false;
  }

  /** The mailbox as the API answers the switch: with `ai_triage_enabled` set. */
  setMailbox(
    organizationId: number,
    mailbox: Mailbox,
    viewer: MailboxViewer,
    enabled: boolean
  ): { ok: true; value: Mailbox } | { ok: false; error: MockTriageError } {
    if (!maySwitchMailboxTriage(mailbox, viewer)) return { ok: false, error: 'forbidden' };
    const enabledIds = this.mailboxes.get(organizationId) ?? new Set<number>();
    if (enabled) enabledIds.add(mailbox.id);
    else enabledIds.delete(mailbox.id);
    this.mailboxes.set(organizationId, enabledIds);
    return { ok: true, value: { ...mailbox, ai_triage_enabled: enabled } };
  }

  /** Needs the organization switch; the caller has already checked `view` and the direction. */
  override(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    category: MailTriageOverrideCategory,
    dueAt: string | null
  ): { ok: true; value: MailTriage } | { ok: false; error: MockTriageError } {
    if (!this.enabled(organizationId)) return { ok: false, error: 'unavailable' };
    this.overrides.set(key(organizationId, mailboxId, messageId), { category, dueAt });
    return { ok: true, value: { category, reason: '', due_at: dueAt, source: 'manual' } };
  }

  /** The row's result while every switch is on; outbound mail is never triaged. */
  resultFor(organizationId: number, item: InboxItem): MailTriage | null {
    if (
      !this.enabled(organizationId) ||
      item.direction !== 'inbound' ||
      !this.mailboxEnabled(organizationId, item.mailbox_id)
    ) {
      return null;
    }
    const manual = this.overrides.get(key(organizationId, item.mailbox_id, item.id));
    if (manual) {
      return { category: manual.category, reason: '', due_at: manual.dueAt, source: 'manual' };
    }
    return fakeTriage(`${item.subject}\n${item.snippet}`);
  }

  /** The rows with their results; every row is `null` while the organization switch is off. */
  decorate(organizationId: number, items: readonly InboxItem[]): InboxItem[] {
    return items.map(item => ({ ...item, triage: this.resultFor(organizationId, item) }));
  }

  /** Counts the given unread inbound rows by category; null while the organization switch is off. */
  counts(organizationId: number, unreadInbox: readonly InboxItem[]): InboxTriageCounts | null {
    if (!this.enabled(organizationId)) return null;
    const counts: InboxTriageCounts = { reply: 0, deadline: 0, fyi: 0, review: 0 };
    for (const item of unreadInbox) {
      const result = this.resultFor(organizationId, item);
      if (result) counts[result.category] += 1;
    }
    return counts;
  }

  matches(organizationId: number, item: InboxItem, categories: readonly MailTriageCategory[]) {
    const result = this.resultFor(organizationId, item);
    return result !== null && categories.includes(result.category);
  }

  /** A short canned reply naming the message it answers; the caller checked `send` and drafts. */
  draft(message: MailMessage): MailReplyDraft {
    const subject = message.subject || '(no subject)';
    return {
      body: `Thanks for your message about "${subject}". I will get back to you shortly.`,
      basis: [{ kind: 'message', id: message.id, label: message.subject }],
    };
  }

  reset(): void {
    this.settings.clear();
    this.mailboxes.clear();
    this.overrides.clear();
  }
}

function key(organizationId: number, mailboxId: number, messageId: number): string {
  return `${organizationId}:${mailboxId}:${messageId}`;
}

export const mockTriageStore = new MockTriageStore();
