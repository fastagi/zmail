'use client';

import { useDeferredValue, useState } from 'react';

import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useMailContactLists, useMailContacts } from '@/features/mailbox/hooks/use-mail-contacts';
import type { MailContact, MailContactStatus } from '@/features/mailbox/types';

/**
 * Headless state and query orchestration for the contacts workspace: active list selection,
 * deferred search and status filters, pagination, and dialog visibility.
 */
export function useContactsWorkspace() {
  const organizationId = useMailOrganizationId();
  const lists = useMailContactLists(organizationId);
  const [listId, setListId] = useState<number | undefined>(undefined);
  const [query, setQuery] = useState('');
  const [status, setStatus] = useState<MailContactStatus | undefined>(undefined);
  const [page, setPage] = useState(1);
  const [editing, setEditing] = useState<MailContact | 'new' | null>(null);
  const [importing, setImporting] = useState(false);
  const deferredQuery = useDeferredValue(query.trim());
  const filters = {
    ...(deferredQuery === '' ? {} : { q: deferredQuery }),
    ...(status ? { status } : {}),
    ...(listId === undefined ? {} : { listId }),
  };
  const contacts = useMailContacts(organizationId, filters, page);
  const allLists = lists.data ?? [];
  const selectedList = allLists.find(list => list.id === listId) ?? null;
  const hasSearchOrStatus = query.trim() !== '' || status !== undefined;
  const hasDeferredSearchOrStatus = deferredQuery !== '' || status !== undefined;
  const isFilteredEmpty =
    !contacts.isPending &&
    !contacts.error &&
    contacts.data !== undefined &&
    contacts.data.items.length === 0 &&
    hasDeferredSearchOrStatus;

  const clearFilters = () => {
    setQuery('');
    setStatus(undefined);
    setPage(1);
  };

  const selectList = (id: number | undefined) => {
    setListId(id);
    setPage(1);
  };

  const updateQuery = (value: string) => {
    setQuery(value);
    setPage(1);
  };

  const updateStatus = (value: MailContactStatus | undefined) => {
    setStatus(value);
    setPage(1);
  };

  return {
    lists,
    allLists,
    selectedList,
    listId,
    query,
    status,
    contacts,
    editing,
    importing,
    hasSearchOrStatus,
    hasDeferredSearchOrStatus,
    isFilteredEmpty,
    setPage,
    setEditing,
    setImporting,
    clearFilters,
    selectList,
    updateQuery,
    updateStatus,
  };
}
