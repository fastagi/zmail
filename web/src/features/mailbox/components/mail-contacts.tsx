'use client';

import {
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
} from '@/features/mailbox/components/mail-organization';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { isOrganizationLost } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

import { ContactDialog } from './contacts/contact-dialog';
import { ContactsTable } from './contacts/contacts-table';
import { ContactsToolbar } from './contacts/contacts-toolbar';
import { ImportDialog } from './contacts/import-dialog';
import { ListsPanel, SelectedListActions } from './contacts/lists-panel';
import { useContactsWorkspace } from './contacts/use-contacts-workspace';

/**
 * Contacts console: the organization's contacts with search and filters, lists on the side, a
 * contact editor, and CSV import with a per-row report.
 */
export function MailContacts() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader
        title={t('contacts.title')}
        description={t('contacts.description')}
        scope={scope}
      />
      <MailOrganizationBoundary scope={scope}>
        {() => <ContactsWorkspace />}
      </MailOrganizationBoundary>
    </div>
  );
}

function ContactsWorkspace() {
  const {
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
  } = useContactsWorkspace();

  if (contacts.error && isOrganizationLost(contacts.error)) return <OrganizationLostState />;

  return (
    <div className="grid gap-6 lg:grid-cols-[15rem_minmax(0,1fr)]">
      <ListsPanel
        lists={allLists}
        loading={lists.isPending}
        selectedId={listId}
        onSelect={selectList}
      />
      <section className="min-w-0 space-y-4">
        <ContactsToolbar
          query={query}
          status={status}
          showClearFilters={hasSearchOrStatus && !isFilteredEmpty}
          onQuery={updateQuery}
          onStatus={updateStatus}
          onClearFilters={clearFilters}
          onAdd={() => setEditing('new')}
          onImport={() => setImporting(true)}
        />
        {selectedList ? (
          <SelectedListActions
            key={selectedList.id}
            list={selectedList}
            onDeleted={() => selectList(undefined)}
          />
        ) : null}
        <ContactsTable
          query={contacts}
          lists={allLists}
          selectedListId={listId}
          filtered={hasDeferredSearchOrStatus}
          onClearFilters={clearFilters}
          onEdit={contact => setEditing(contact)}
          onAdd={() => setEditing('new')}
          onPage={setPage}
        />
      </section>
      {editing ? (
        <ContactDialog
          contact={editing === 'new' ? null : editing}
          lists={allLists}
          defaultListId={listId}
          onClose={() => setEditing(null)}
        />
      ) : null}
      {importing ? (
        <ImportDialog lists={allLists} defaultListId={listId} onClose={() => setImporting(false)} />
      ) : null}
    </div>
  );
}
