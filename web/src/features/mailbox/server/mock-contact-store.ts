import 'server-only';

import {
  MAIL_CONTACT_IMPORT_MAX_BYTES,
  MAIL_CONTACT_IMPORT_MAX_ROWS,
  MAIL_CONTACT_LIST_NAME_MAX_LENGTH,
  MAIL_CONTACT_MAX_LISTS,
  MAIL_CONTACT_MAX_PROPERTIES,
  MAIL_CONTACT_NAME_MAX_LENGTH,
  MAIL_CONTACT_PROPERTY_MAX_LENGTH,
  MAIL_CONTACT_RESERVED_FIELDS,
  MAIL_TEMPLATE_VARIABLE_PATTERN,
  normalizeRecipient,
} from '@/features/mailbox/schemas';
import type {
  MailContact,
  MailContactFilters,
  MailContactImport,
  MailContactImportInput,
  MailContactImportSkipReason,
  MailContactInput,
  MailContactList,
  MailContactPage,
  MailContactStatus,
} from '@/features/mailbox/types';

/**
 * Development-only contacts and lists, isolated per organization. It follows the rules of
 * `contracts/MAILBOXES.md` for contacts, lists, and CSV import so the console behaves the same
 * against the mock and the API.
 */

const maxReportedSkips = 100;
const maxImportColumns = 60;

interface OrganizationContacts {
  contacts: Map<number, MailContact>;
  lists: Map<number, Omit<MailContactList, 'contact_count'>>;
}

export type MockContactResult =
  | { ok: true; contact: MailContact }
  | { ok: false; error: 'not_found' | 'exists' | 'invalid' | 'list_not_found' };

export type MockContactListResult =
  | { ok: true; list: MailContactList }
  | { ok: false; error: 'not_found' | 'invalid' | 'taken' | 'limit' };

export type MockContactImportResult =
  { ok: true; result: MailContactImport } | { ok: false; error: 'invalid' | 'list_not_found' };

interface ImportRow {
  email: string;
  first_name: string;
  last_name: string;
  properties: Record<string, string>;
}

export class MockContactStore {
  private nextId = 1;
  private organizations = new Map<number, OrganizationContacts>();

  constructor(private readonly now: () => Date = () => new Date()) {}

  list(
    organizationId: number,
    filters: MailContactFilters,
    page: number,
    perPage: number
  ): MailContactPage {
    const needle = filters.q?.trim().toLowerCase() ?? '';
    const matching = [...this.of(organizationId).contacts.values()]
      .filter(
        contact =>
          (needle === '' ||
            [contact.email, contact.first_name, contact.last_name].some(field =>
              field.toLowerCase().includes(needle)
            )) &&
          (filters.status === undefined || contact.status === filters.status) &&
          (filters.listId === undefined || contact.list_ids.includes(filters.listId))
      )
      .sort((a, b) => b.id - a.id);
    const total = matching.length;
    const lastPage = Math.max(1, Math.ceil(total / perPage));
    const start = (page - 1) * perPage;
    const items = matching.slice(start, start + perPage);
    return {
      items,
      meta: {
        current_page: page,
        per_page: perPage,
        total,
        last_page: lastPage,
        from: items.length > 0 ? start + 1 : 0,
        to: start + items.length,
      },
      links: { first: '', last: '', prev: null, next: null },
    };
  }

  create(organizationId: number, input: MailContactInput): MockContactResult {
    const state = this.of(organizationId);
    const email = normalizeRecipient(input.email ?? '');
    const fields = cleanFields(input);
    if (!email || !fields) return { ok: false, error: 'invalid' };
    if ([...state.contacts.values()].some(contact => contact.email === email)) {
      return { ok: false, error: 'exists' };
    }
    if (!fields.list_ids.every(id => state.lists.has(id))) {
      return { ok: false, error: 'list_not_found' };
    }
    const now = this.timestamp();
    const status = input.status ?? 'subscribed';
    const contact: MailContact = {
      id: this.nextId++,
      email,
      ...fields,
      status,
      status_reason: status === 'unsubscribed' ? 'manual' : '',
      created_at: now,
      updated_at: now,
    };
    state.contacts.set(contact.id, contact);
    return { ok: true, contact };
  }

  update(organizationId: number, contactId: number, input: MailContactInput): MockContactResult {
    const state = this.of(organizationId);
    const current = state.contacts.get(contactId);
    if (!current) return { ok: false, error: 'not_found' };
    const fields = cleanFields(input);
    if (!fields) return { ok: false, error: 'invalid' };
    if (!fields.list_ids.every(id => state.lists.has(id))) {
      return { ok: false, error: 'list_not_found' };
    }
    const changed = input.status !== undefined && input.status !== current.status;
    const contact: MailContact = {
      ...current,
      ...fields,
      status: changed ? (input.status as MailContact['status']) : current.status,
      status_reason: changed
        ? input.status === 'unsubscribed'
          ? 'manual'
          : ''
        : current.status_reason,
      updated_at: this.timestamp(),
    };
    state.contacts.set(contactId, contact);
    return { ok: true, contact };
  }

  remove(organizationId: number, contactId: number): boolean {
    return this.of(organizationId).contacts.delete(contactId);
  }

  lists(organizationId: number): MailContactList[] {
    const state = this.of(organizationId);
    return [...state.lists.values()]
      .map(list => ({
        ...list,
        contact_count: [...state.contacts.values()].filter(contact =>
          contact.list_ids.includes(list.id)
        ).length,
      }))
      .sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : a.id - b.id));
  }

  saveList(organizationId: number, rawName: string, listId?: number): MockContactListResult {
    const state = this.of(organizationId);
    const name = rawName.trim().split(/\s+/u).filter(Boolean).join(' ');
    if (name === '' || Array.from(name).length > MAIL_CONTACT_LIST_NAME_MAX_LENGTH) {
      return { ok: false, error: 'invalid' };
    }
    if (listId !== undefined && !state.lists.has(listId)) return { ok: false, error: 'not_found' };
    if ([...state.lists.values()].some(list => list.name === name && list.id !== listId)) {
      return { ok: false, error: 'taken' };
    }
    if (listId === undefined && state.lists.size >= MAIL_CONTACT_MAX_LISTS) {
      return { ok: false, error: 'limit' };
    }
    const existing = listId === undefined ? undefined : state.lists.get(listId);
    const list = existing
      ? { ...existing, name }
      : { id: this.nextId++, name, created_at: this.timestamp() };
    state.lists.set(list.id, list);
    const counted = this.lists(organizationId).find(candidate => candidate.id === list.id);
    return counted ? { ok: true, list: counted } : { ok: false, error: 'not_found' };
  }

  removeList(organizationId: number, listId: number): boolean {
    const state = this.of(organizationId);
    if (!state.lists.delete(listId)) return false;
    for (const contact of state.contacts.values()) {
      contact.list_ids = contact.list_ids.filter(id => id !== listId);
    }
    return true;
  }

  import(organizationId: number, input: MailContactImportInput): MockContactImportResult {
    const state = this.of(organizationId);
    if (input.list_id !== undefined && !state.lists.has(input.list_id)) {
      return { ok: false, error: 'list_not_found' };
    }
    const parsed = parseImport(input.csv);
    if (!parsed) return { ok: false, error: 'invalid' };
    const { rows, result } = parsed;
    const byEmail = new Map([...state.contacts.values()].map(contact => [contact.email, contact]));
    const now = this.timestamp();
    for (const row of rows) {
      const existing = byEmail.get(row.email);
      let contact: MailContact;
      if (existing) {
        // An empty cell keeps the stored value; the status of an existing contact never changes.
        contact = {
          ...existing,
          first_name: row.first_name || existing.first_name,
          last_name: row.last_name || existing.last_name,
          properties: { ...existing.properties, ...row.properties },
          updated_at: now,
        };
        result.updated++;
      } else {
        contact = {
          id: this.nextId++,
          email: row.email,
          first_name: row.first_name,
          last_name: row.last_name,
          properties: row.properties,
          status: 'subscribed',
          status_reason: '',
          list_ids: [],
          created_at: now,
          updated_at: now,
        };
        result.created++;
      }
      if (input.list_id !== undefined && !contact.list_ids.includes(input.list_id)) {
        contact.list_ids = [...contact.list_ids, input.list_id].sort((a, b) => a - b);
      }
      state.contacts.set(contact.id, contact);
      byEmail.set(contact.email, contact);
    }
    return { ok: true, result };
  }

  hasList(organizationId: number, listId: number): boolean {
    return this.of(organizationId).lists.has(listId);
  }

  /** The subscribed contacts of a list, oldest first. */
  subscribedIn(organizationId: number, listId: number): MailContact[] {
    return [...this.of(organizationId).contacts.values()]
      .filter(contact => contact.status === 'subscribed' && contact.list_ids.includes(listId))
      .sort((a, b) => a.id - b.id);
  }

  /** The contact's current status, or null once it is deleted. */
  statusOf(organizationId: number, contactId: number): MailContactStatus | null {
    return this.of(organizationId).contacts.get(contactId)?.status ?? null;
  }

  /** Applies an unsubscribe link; unknown or non-subscribed contacts are left alone. */
  unsubscribe(organizationId: number, contactId: number): void {
    const contact = this.of(organizationId).contacts.get(contactId);
    if (contact?.status === 'subscribed') {
      this.of(organizationId).contacts.set(contactId, {
        ...contact,
        status: 'unsubscribed',
        status_reason: 'unsubscribe_link',
        updated_at: this.timestamp(),
      });
    }
  }

  /** Test seam: forget every contact and list. */
  reset(): void {
    this.nextId = 1;
    this.organizations.clear();
  }

  private of(organizationId: number): OrganizationContacts {
    const existing = this.organizations.get(organizationId);
    if (existing) return existing;
    const created: OrganizationContacts = { contacts: new Map(), lists: new Map() };
    this.organizations.set(organizationId, created);
    return created;
  }

  private timestamp(): string {
    return this.now().toISOString();
  }
}

export const mockContactStore = new MockContactStore();

function cleanName(raw: string | undefined): string | null {
  const name = (raw ?? '').trim().split(/\s+/u).filter(Boolean).join(' ');
  return Array.from(name).length > MAIL_CONTACT_NAME_MAX_LENGTH ? null : name;
}

function cleanProperties(
  raw: Readonly<Record<string, string>>
):
  { ok: true; value: Record<string, string> } | { ok: false; reason: MailContactImportSkipReason } {
  const properties: Record<string, string> = {};
  for (const [name, rawValue] of Object.entries(raw)) {
    if (
      !MAIL_TEMPLATE_VARIABLE_PATTERN.test(name) ||
      (MAIL_CONTACT_RESERVED_FIELDS as readonly string[]).includes(name)
    ) {
      return { ok: false, reason: 'invalid_property' };
    }
    const value = rawValue.trim();
    if (Array.from(value).length > MAIL_CONTACT_PROPERTY_MAX_LENGTH) {
      return { ok: false, reason: 'invalid_property' };
    }
    if (value !== '') properties[name] = value;
  }
  if (Object.keys(properties).length > MAIL_CONTACT_MAX_PROPERTIES) {
    return { ok: false, reason: 'too_many_properties' };
  }
  return { ok: true, value: properties };
}

function cleanFields(
  input: MailContactInput
): Pick<MailContact, 'first_name' | 'last_name' | 'properties' | 'list_ids'> | null {
  const firstName = cleanName(input.first_name);
  const lastName = cleanName(input.last_name);
  const properties = cleanProperties(input.properties ?? {});
  const listIds = [...new Set(input.list_ids ?? [])].sort((a, b) => a - b);
  if (firstName === null || lastName === null || !properties.ok) return null;
  if (listIds.length > MAIL_CONTACT_MAX_LISTS) return null;
  return {
    first_name: firstName,
    last_name: lastName,
    properties: properties.value,
    list_ids: listIds,
  };
}

/** RFC 4180 records with the line each starts on; null for broken quoting. */
export function parseCsv(text: string): { line: number; fields: string[] }[] | null {
  const records: { line: number; fields: string[] }[] = [];
  let fields: string[] = [];
  let field = '';
  let quoted = false;
  let line = 1;
  let recordLine = 1;
  let index = 0;
  while (index < text.length) {
    const char = text[index];
    if (quoted) {
      if (char === '"') {
        if (text[index + 1] === '"') {
          field += '"';
          index += 2;
          continue;
        }
        quoted = false;
        index++;
        continue;
      }
      if (char === '\n') line++;
      field += char;
      index++;
      continue;
    }
    if (char === '"') {
      if (field !== '') return null;
      quoted = true;
      index++;
      continue;
    }
    if (char === ',') {
      fields.push(field);
      field = '';
      index++;
      continue;
    }
    if (char === '\r' || char === '\n') {
      fields.push(field);
      records.push({ line: recordLine, fields });
      fields = [];
      field = '';
      if (char === '\r' && text[index + 1] === '\n') index++;
      index++;
      line++;
      recordLine = line;
      continue;
    }
    field += char;
    index++;
  }
  if (quoted) return null;
  if (field !== '' || fields.length > 0) {
    fields.push(field);
    records.push({ line: recordLine, fields });
  }
  return records;
}

type ImportColumn =
  { kind: 'email' | 'first_name' | 'last_name' } | { kind: 'property'; name: string } | null;

function importColumns(header: string[]): { columns: ImportColumn[]; ignored: string[] } | null {
  if (header.length > maxImportColumns) return null;
  const seen = new Set<string>();
  const ignored: string[] = [];
  let hasEmail = false;
  const columns = header.map((raw): ImportColumn => {
    let name = raw.trim().toLowerCase().replace(/[ -]/gu, '_');
    if (name === 'e_mail' || name === 'email_address') name = 'email';
    if (name !== '' && seen.has(name)) throw new DuplicateColumn();
    seen.add(name);
    if (name === 'email') {
      hasEmail = true;
      return { kind: 'email' };
    }
    if (name === 'first_name' || name === 'last_name') return { kind: name };
    if (MAIL_TEMPLATE_VARIABLE_PATTERN.test(name)) return { kind: 'property', name };
    if (raw.trim() !== '') ignored.push(raw.trim().slice(0, 100));
    return null;
  });
  return hasEmail ? { columns, ignored } : null;
}

class DuplicateColumn extends Error {}

function parseImport(text: string): { rows: ImportRow[]; result: MailContactImport } | null {
  if (new TextEncoder().encode(text).length > MAIL_CONTACT_IMPORT_MAX_BYTES) return null;
  const records = parseCsv(text.replace(/^\uFEFF/u, ''));
  if (!records || records.length === 0) return null;
  let mapped: ReturnType<typeof importColumns>;
  try {
    mapped = importColumns(records[0]?.fields ?? []);
  } catch {
    return null;
  }
  if (!mapped) return null;
  const result: MailContactImport = {
    rows: 0,
    created: 0,
    updated: 0,
    skipped_count: 0,
    skipped: [],
    ignored_columns: mapped.ignored,
  };
  const skip = (line: number, reason: MailContactImportSkipReason) => {
    result.skipped_count++;
    if (result.skipped.length < maxReportedSkips) result.skipped.push({ line, reason });
  };
  const seen = new Set<string>();
  const rows: ImportRow[] = [];
  for (const record of records.slice(1)) {
    if (record.fields.every(value => value.trim() === '')) continue;
    result.rows++;
    if (result.rows > MAIL_CONTACT_IMPORT_MAX_ROWS) return null;
    const raw: ImportRow = { email: '', first_name: '', last_name: '', properties: {} };
    record.fields.forEach((value, index) => {
      const column = mapped?.columns[index];
      if (!column) return;
      if (column.kind === 'property') raw.properties[column.name] = value;
      else raw[column.kind] = value;
    });
    const email = normalizeRecipient(raw.email);
    if (!email) {
      skip(record.line, 'invalid_email');
      continue;
    }
    const firstName = cleanName(raw.first_name);
    const lastName = cleanName(raw.last_name);
    if (firstName === null || lastName === null) {
      skip(record.line, 'invalid_name');
      continue;
    }
    const properties = cleanProperties(raw.properties);
    if (!properties.ok) {
      skip(record.line, properties.reason);
      continue;
    }
    if (seen.has(email)) {
      skip(record.line, 'duplicate');
      continue;
    }
    seen.add(email);
    rows.push({ email, first_name: firstName, last_name: lastName, properties: properties.value });
  }
  return { rows, result };
}
