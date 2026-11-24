import fs from 'node:fs';
import path from 'node:path';
import type {
  CachedMailMessage,
  DeltaCursorState,
  ListMessagesQuery,
  LocalSearchHit,
  LocalSearchQuery,
  LocalSearchResult,
  LocalStoreStats,
} from '../shared/ipc-channels.ts';

/**
 * Persistence seam for offline cache snapshots.
 * Defaults to atomic JSON snapshots in portable builds and is structured to
 * back onto native SQLite/FTS5 storage in packaged Electron builds.
 */
export interface LocalStorePersistence {
  load(): LocalStoreSnapshot | null;
  save(snapshot: LocalStoreSnapshot): void;
}

export interface LocalStoreSnapshot {
  version: 1;
  messages: CachedMailMessage[];
  cursors: DeltaCursorState[];
}

export function createFileStorePersistence(filePath: string): LocalStorePersistence {
  return {
    load(): LocalStoreSnapshot | null {
      if (!fs.existsSync(filePath)) return null;
      try {
        const parsed = JSON.parse(fs.readFileSync(filePath, 'utf8')) as Partial<LocalStoreSnapshot>;
        if (parsed.version !== 1 || !Array.isArray(parsed.messages) || !Array.isArray(parsed.cursors)) {
          return null;
        }
        return { version: 1, messages: parsed.messages, cursors: parsed.cursors };
      } catch {
        return null;
      }
    },
    save(snapshot: LocalStoreSnapshot): void {
      fs.mkdirSync(path.dirname(filePath), { recursive: true, mode: 0o700 });
      const tmpPath = `${filePath}.${process.pid}.${Date.now()}.tmp`;
      fs.writeFileSync(tmpPath, JSON.stringify(snapshot), { encoding: 'utf8', mode: 0o600 });
      fs.renameSync(tmpPath, filePath);
    },
  };
}

function compositeKey(accountId: string, messageId: number): string {
  return `${accountId}:${messageId}`;
}

/**
 * Tokenizes Latin words, email fragments, numbers, and CJK unigrams/bigrams
 * for fast offline full-text lookup.
 */
export function tokenizeText(input: string): string[] {
  const normalized = input.toLowerCase().trim();
  if (!normalized) return [];
  const tokens = new Set<string>();

  for (const raw of normalized.match(/[a-z0-9][a-z0-9._+-]*/g) ?? []) {
    tokens.add(raw);
    if (/[._+@ -]/.test(raw)) {
      for (const part of raw.split(/[@._+-]+/)) {
        if (part.length >= 2) tokens.add(part);
      }
    }
  }

  for (const run of normalized.match(/[\u4e00-\u9fff]+/g) ?? []) {
    for (let i = 0; i < run.length; i += 1) {
      const char = run[i];
      if (char) tokens.add(char);
      if (i + 1 < run.length) tokens.add(run.slice(i, i + 2));
    }
  }

  return Array.from(tokens);
}

function compareNewestFirst(a: CachedMailMessage, b: CachedMailMessage): number {
  const delta = Date.parse(b.createdAt) - Date.parse(a.createdAt);
  return !Number.isNaN(delta) && delta !== 0 ? delta : b.id - a.id;
}

function cloneMessage(message: CachedMailMessage): CachedMailMessage {
  return { ...message, to: [...message.to], cc: [...message.cc] };
}

function buildHighlight(message: CachedMailMessage, terms: string[]): string {
  const source = message.snippet || message.bodyText || message.subject;
  if (!source) return '';
  const lower = source.toLowerCase();
  let firstIndex = -1;
  for (const term of terms) {
    const idx = lower.indexOf(term);
    if (idx !== -1 && (firstIndex === -1 || idx < firstIndex)) firstIndex = idx;
  }
  if (firstIndex === -1) return source.slice(0, 140);
  const start = Math.max(0, firstIndex - 32);
  const end = Math.min(source.length, start + 140);
  return `${start > 0 ? '...' : ''}${source.slice(start, end)}${end < source.length ? '...' : ''}`;
}

export class LocalMailStore {
  private readonly messages = new Map<string, CachedMailMessage>();
  private readonly messageTokens = new Map<string, Set<string>>();
  private readonly invertedIndex = new Map<string, Set<string>>();
  private readonly cursors = new Map<string, DeltaCursorState>();
  private readonly persistence: LocalStorePersistence | null;

  constructor(persistence: LocalStorePersistence | null = null) {
    this.persistence = persistence;
    if (persistence) {
      const snapshot = persistence.load();
      if (snapshot) {
        this.hydrate(snapshot);
      }
    }
  }

  upsertMessages(items: readonly CachedMailMessage[]): number {
    for (const item of items) {
      const key = compositeKey(item.accountId, item.id);
      this.removeTokensForKey(key);

      const copy = cloneMessage(item);
      this.messages.set(key, copy);
      this.indexMessage(key, copy);
    }
    this.flush();
    return items.length;
  }

  deleteMessage(accountId: string, messageId: number): boolean {
    const key = compositeKey(accountId, messageId);
    if (!this.messages.has(key)) {
      return false;
    }
    this.removeTokensForKey(key);
    this.messages.delete(key);
    this.flush();
    return true;
  }

  listMessages(query: ListMessagesQuery = {}): CachedMailMessage[] {
    const limit = query.limit ?? 100;
    const filtered: CachedMailMessage[] = [];
    for (const message of this.messages.values()) {
      if (query.accountId && message.accountId !== query.accountId) continue;
      if (query.placement && message.placement !== query.placement) continue;
      if (query.unreadOnly && message.read) continue;
      if (query.starredOnly && !message.starred) continue;
      filtered.push(cloneMessage(message));
    }
    filtered.sort(compareNewestFirst);
    return filtered.slice(0, Math.max(0, limit));
  }

  search(query: LocalSearchQuery): LocalSearchResult {
    const startedAt = performance.now();
    const limit = query.limit ?? 50;
    const terms = tokenizeText(query.query);

    if (terms.length === 0) {
      const recent = this.listMessages({
        accountId: query.accountId,
        placement: query.placement,
        unreadOnly: query.unreadOnly,
        starredOnly: query.starredOnly,
        limit,
      });
      return {
        query: query.query,
        total: recent.length,
        elapsedMs: Number((performance.now() - startedAt).toFixed(2)),
        hits: recent.map((message) => ({
          message,
          score: 1,
          matchedTerms: [],
          highlight: message.snippet || message.bodyText.slice(0, 140),
        })),
      };
    }

    let candidateKeys: Set<string> | null = null;
    for (const term of terms) {
      const termMatches = this.findKeysByPrefix(term);
      if (candidateKeys === null) {
        candidateKeys = termMatches;
      } else {
        const next = new Set<string>();
        for (const key of candidateKeys) {
          if (termMatches.has(key)) {
            next.add(key);
          }
        }
        candidateKeys = next;
      }
      if (candidateKeys.size === 0) {
        break;
      }
    }

    const hits: LocalSearchHit[] = [];
    for (const key of candidateKeys ?? []) {
      const message = this.messages.get(key);
      if (!message) continue;
      if (query.accountId && message.accountId !== query.accountId) continue;
      if (query.placement && message.placement !== query.placement) continue;
      if (query.unreadOnly && message.read) continue;
      if (query.starredOnly && !message.starred) continue;

      const score = this.scoreMessage(message, terms);
      hits.push({
        message: cloneMessage(message),
        score,
        matchedTerms: terms,
        highlight: buildHighlight(message, terms),
      });
    }

    hits.sort((a, b) => {
      if (b.score !== a.score) return b.score - a.score;
      return compareNewestFirst(a.message, b.message);
    });

    return {
      query: query.query,
      total: hits.length,
      elapsedMs: Number((performance.now() - startedAt).toFixed(2)),
      hits: hits.slice(0, Math.max(0, limit)),
    };
  }

  getDeltaCursor(accountId: string): DeltaCursorState {
    const existing = this.cursors.get(accountId);
    if (existing) {
      return { ...existing };
    }
    return { accountId, token: null, updatedAt: null };
  }

  setDeltaCursor(cursor: DeltaCursorState): DeltaCursorState {
    const next: DeltaCursorState = {
      accountId: cursor.accountId,
      token: cursor.token,
      updatedAt: cursor.updatedAt ?? new Date().toISOString(),
    };
    this.cursors.set(cursor.accountId, next);
    this.flush();
    return { ...next };
  }

  resetAccount(accountId: string): void {
    for (const [key, message] of this.messages.entries()) {
      if (message.accountId === accountId) {
        this.removeTokensForKey(key);
        this.messages.delete(key);
      }
    }
    this.cursors.delete(accountId);
    this.flush();
  }

  getUnreadCount(accountId?: string): number {
    let unread = 0;
    for (const message of this.messages.values()) {
      if (accountId && message.accountId !== accountId) continue;
      if (!message.read && message.placement !== 'trash') {
        unread += 1;
      }
    }
    return unread;
  }

  getStats(): LocalStoreStats {
    const accountIds = new Set<string>();
    let unreadMessages = 0;
    for (const message of this.messages.values()) {
      accountIds.add(message.accountId);
      if (!message.read && message.placement !== 'trash') {
        unreadMessages += 1;
      }
    }
    return {
      totalMessages: this.messages.size,
      unreadMessages,
      indexedTerms: this.invertedIndex.size,
      accountCount: accountIds.size,
    };
  }

  serialize(): LocalStoreSnapshot {
    return {
      version: 1,
      messages: Array.from(this.messages.values()).map(cloneMessage),
      cursors: Array.from(this.cursors.values()).map((cursor) => ({ ...cursor })),
    };
  }

  hydrate(snapshot: LocalStoreSnapshot): void {
    this.messages.clear();
    this.messageTokens.clear();
    this.invertedIndex.clear();
    this.cursors.clear();

    for (const message of snapshot.messages) {
      const key = compositeKey(message.accountId, message.id);
      const copy = cloneMessage(message);
      this.messages.set(key, copy);
      this.indexMessage(key, copy);
    }
    for (const cursor of snapshot.cursors) {
      this.cursors.set(cursor.accountId, { ...cursor });
    }
  }

  flush(): void {
    if (this.persistence) {
      this.persistence.save(this.serialize());
    }
  }

  private indexMessage(key: string, message: CachedMailMessage): void {
    const combined = [
      message.subject,
      message.from,
      message.to.join(' '),
      message.cc.join(' '),
      message.snippet,
      message.bodyText,
    ].join(' ');
    const tokenSet = new Set(tokenizeText(combined));
    this.messageTokens.set(key, tokenSet);

    for (const token of tokenSet) {
      let bucket = this.invertedIndex.get(token);
      if (!bucket) {
        bucket = new Set<string>();
        this.invertedIndex.set(token, bucket);
      }
      bucket.add(key);
    }
  }

  private removeTokensForKey(key: string): void {
    const existingTokens = this.messageTokens.get(key);
    if (!existingTokens) return;
    for (const token of existingTokens) {
      const bucket = this.invertedIndex.get(token);
      if (!bucket) continue;
      bucket.delete(key);
      if (bucket.size === 0) {
        this.invertedIndex.delete(token);
      }
    }
    this.messageTokens.delete(key);
  }

  private findKeysByPrefix(term: string): Set<string> {
    const matches = new Set<string>();
    const exact = this.invertedIndex.get(term);
    if (exact) {
      for (const key of exact) matches.add(key);
    }
    for (const [token, keys] of this.invertedIndex.entries()) {
      if (token.startsWith(term) && token !== term) {
        for (const key of keys) matches.add(key);
      }
    }
    return matches;
  }

  private scoreMessage(message: CachedMailMessage, terms: readonly string[]): number {
    const subject = message.subject.toLowerCase();
    const from = message.from.toLowerCase();
    const recipients = `${message.to.join(' ')} ${message.cc.join(' ')}`.toLowerCase();
    const body = `${message.snippet} ${message.bodyText}`.toLowerCase();

    let score = 0;
    for (const term of terms) {
      if (subject.includes(term)) score += 6;
      if (from.includes(term)) score += 4;
      if (recipients.includes(term)) score += 3;
      if (body.includes(term)) score += 1;
    }
    return score;
  }
}
