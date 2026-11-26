'use client';

import { lazy, Suspense } from 'react';
import { Mail, Plus } from 'lucide-react';

import { Button } from '@/components/ui/button';
import {
  ComposeDialog,
  ExternalMailboxDialogs,
  InboxFrame,
  InboxOverlayDialogs,
  InboxSkeleton,
  MailShortcuts,
  ManageMailboxesAction,
} from '@/features/mailbox/components/inbox/inbox-dialogs';
import { InboxListPane } from '@/features/mailbox/components/inbox/inbox-list-pane';
import { InboxReadingSection } from '@/features/mailbox/components/inbox/inbox-reading-section';
import {
  NEW_PARAM,
  OPEN_PARAM,
  type InboxDateRange,
  type ReadyScope,
} from '@/features/mailbox/components/inbox/inbox-url';
import { useInboxWorkspace } from '@/features/mailbox/components/inbox/use-inbox-workspace';
import { InboxSidebar } from '@/features/mailbox/components/inbox-sidebar';
import {
  MailOrganizationBoundary,
  MailOrganizationName,
  MailPageHeader,
  OrganizationLostState,
  mailPageClassName,
} from '@/features/mailbox/components/mail-organization';
import { EmptyState, ErrorAlert } from '@/features/mailbox/components/mail-shared';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { useIsPersonalMode } from '@/features/mailbox/stores/workspace-mode';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

export { OPEN_PARAM, type InboxDateRange };

// Only someone without a team sees it, so it loads on demand.
const TeamSetupCard = lazy(async () => {
  const feature = await import('@/features/organization/components/team-setup-card');
  return { default: feature.TeamSetupCard };
});
// Only someone in Personal Mode empty state who has not claimed yet loads it on demand.
const ClaimPublicMailboxCard = lazy(async () => ({
  default: (await import('@/features/mailbox/components/claim-public-mailbox-card'))
    .ClaimPublicMailboxCard,
}));

/**
 * The default mail page: every mailbox the user reads in one list, as three panes (§11.0: the
 * 248px sidebar, the 470px list grouped by thread, and the reading pane), filling the console's
 * content area. One mailbox's own workspace stays one click away under `?mailbox=`.
 *
 * Responsive (§11.11): three panes from 1024px (the console navigation folds to icons
 * here); below 1024px the sidebar folds to an icon rail and the list and the open mail take
 * turns, with a way back; below 768px the sidebar opens as a sheet. While the organization and
 * the mailboxes load, the three panes show as skeletons; without an organization, and when
 * there is nothing to list, the page keeps the console's ordinary header and frame.
 */
export function InboxWorkspace() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  // The three panes stand while the organization resolves, so the page does not change shape.
  if (scope.status === 'loading') return <InboxSkeleton />;
  if (scope.status !== 'ready') {
    return (
      <div className={cn(mailPageClassName, 'space-y-5')}>
        <MailPageHeader
          title={t('inbox.title')}
          description={t('inbox.description')}
          scope={scope}
        />
        {/* Without a team the first step is to name one, right here. */}
        {scope.status === 'empty' ? (
          <Suspense fallback={null}>
            <TeamSetupCard className="mx-auto max-w-xl" />
          </Suspense>
        ) : (
          <MailOrganizationBoundary scope={scope}>{() => null}</MailOrganizationBoundary>
        )}
      </div>
    );
  }
  return (
    <MailOrganizationBoundary scope={scope}>
      {organization => <UnifiedInbox canManage={organization.canManage} scope={scope} />}
    </MailOrganizationBoundary>
  );
}

function UnifiedInbox({ canManage, scope }: { canManage: boolean; scope: ReadyScope }) {
  const t = useT('mailbox');
  const isPersonalMode = useIsPersonalMode();
  const state = useInboxWorkspace({ canManage });

  const externalDialogs = (
    <ExternalMailboxDialogs
      organizationId={state.organizationId}
      connectMounted={state.connectMounted}
      connectSession={state.connectSession}
      connectOpen={state.connectOpen}
      onConnectOpenChange={state.setConnectOpen}
      connectorList={state.connectorList}
      connectPreset={state.connectPreset}
      manageExternalMounted={state.manageExternalMounted}
      manageExternalOpen={state.manageExternalOpen}
      onManageExternalOpenChange={state.setManageExternalOpen}
      connections={state.connections.data}
      connectors={state.connectors.data}
      connectionsError={!state.connections.data && state.connections.isError}
      externalOn={state.externalOn}
      onOpenConnect={state.openConnect}
      returnFocusRef={state.externalReturnFocus}
    />
  );

  if (state.mailboxes.isPending) return <InboxSkeleton />;
  if (isOrganizationLost(state.mailboxes.error) || isOrganizationLost(state.inbox.error)) {
    return (
      <InboxFrame scope={scope}>
        <OrganizationLostState />
      </InboxFrame>
    );
  }
  if (!state.mailboxes.data) {
    return (
      <InboxFrame scope={scope}>
        <ErrorAlert
          message={t(resolveMailboxErrorKey(state.mailboxes.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void state.mailboxes.refetch()}
        />
      </InboxFrame>
    );
  }

  if (state.readable.length === 0) {
    const isPersonalEmpty = isPersonalMode && state.mailboxes.data.length === 0;
    return (
      <InboxFrame scope={scope}>
        {isPersonalEmpty && state.canClaimPublicMailbox && state.publicMailboxOptions ? (
          <Suspense fallback={null}>
            <ClaimPublicMailboxCard
              options={state.publicMailboxOptions}
              organizationId={state.organizationId}
              variant="card"
              onClaimed={mailbox => state.onBox(mailbox.id)}
              className="mx-auto max-w-xl"
            />
          </Suspense>
        ) : null}
        <EmptyState
          icon={<Mail className="size-5" aria-hidden="true" />}
          title={t(isPersonalEmpty ? 'inbox.empty.personalNoMailboxes' : 'inbox.empty.noMailboxes')}
          description={t(
            isPersonalEmpty
              ? 'inbox.empty.personalNoMailboxesHint'
              : canManage
                ? 'inbox.empty.noMailboxesManage'
                : 'inbox.empty.noMailboxesMember'
          )}
          action={
            isPersonalEmpty ? (
              state.externalOn ? (
                <Button
                  type="button"
                  size="default"
                  variant="default"
                  icon={<Plus className="size-4" />}
                  onClick={() => state.openConnect()}
                >
                  {t('external.sidebar.add')}
                </Button>
              ) : undefined
            ) : canManage || state.externalOn ? (
              <div className="flex flex-wrap justify-center gap-2">
                {canManage ? (
                  <ManageMailboxesAction
                    mailboxes={state.mailboxes.data}
                    onRefresh={() => void state.mailboxes.refetch()}
                    autoOpen={state.searchParams.get(NEW_PARAM) === 'mailbox'}
                    onAutoOpened={state.dropNewParam}
                  />
                ) : null}
                {state.externalOn ? (
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    icon={<Plus className="size-4" />}
                    onClick={() => state.openConnect()}
                  >
                    {t('external.sidebar.add')}
                  </Button>
                ) : null}
              </div>
            ) : undefined
          }
        />
        {externalDialogs}
      </InboxFrame>
    );
  }

  if (state.draft?.draft.mode === 'compose' && state.sendableIds.has(state.draft.mailbox.id)) {
    return (
      <Suspense fallback={null}>
        <ComposeDialog
          inline
          mailbox={state.draft.mailbox}
          fromOptions={state.sendable}
          draft={state.draft.draft}
          onClose={() => state.setDraft(null)}
          onReopen={state.reopenDraft(state.draft)}
        />
      </Suspense>
    );
  }

  return (
    <div className="flex h-full min-h-0 bg-bg-canvas text-[13px] leading-normal antialiased">
      <MailShortcuts bindings={state.shortcuts} />
      <aside className="hidden w-14 flex-none flex-col overflow-y-auto px-2 pt-[18px] pb-3.5 md:flex lg:w-[248px] lg:px-3">
        <InboxSidebar
          {...state.sidebarProps}
          rail
          organization={<MailOrganizationName scope={scope} compact />}
          onView={state.onView}
          onBox={state.onBox}
          onRole={state.onRole}
          onCompose={state.compose}
        />
      </aside>

      <InboxListPane
        organizationId={state.organizationId}
        title={state.title}
        subtitle={state.subtitle}
        narrowedTo={state.narrowedTo}
        urlState={state.urlState}
        counts={state.counts.data}
        triageCounts={state.triageCounts}
        assignmentTabs={state.assignmentTabs}
        zoneReady={state.zoneReady}
        timeZone={state.timeZone}
        inboxPending={state.inbox.isPending}
        inboxFetching={
          state.inbox.isFetching || state.counts.isFetching || state.mailboxes.isFetching
        }
        inboxHasData={Boolean(state.inbox.data)}
        inboxError={state.inbox.error}
        resetting={state.resetting}
        pages={state.pages}
        rows={state.rows}
        roleTags={state.roleTags}
        showMailbox={state.showMailbox}
        sendableIds={state.sendableIds}
        openItem={state.openItem}
        readable={state.readable}
        isFetchingNextPage={state.isFetchingNextPage}
        connectionList={state.connectionList}
        connectors={state.connectors.data}
        searchRef={state.searchRef}
        onRefresh={state.refreshListing}
        onRefetchInbox={() => void state.inbox.refetch()}
        onSetListing={state.setListing}
        onBox={state.onBox}
        onOpenRow={state.setOpenParam}
        onLoadMore={state.loadMore}
        onShowSidebar={() => {
          state.setSidebarSheet(true);
          state.setSidebarOpen(true);
        }}
        onReconnectWithPassword={state.openConnect}
        mailboxHref={state.mailboxHref}
        checkedIds={state.checkedIds}
        selectedItems={state.selectedItems}
        bulkPending={state.bulkPending}
        onToggleSelect={state.toggleSelect}
        onToggleSelectAll={state.toggleSelectAll}
        onClearSelection={state.clearSelection}
        onQuickAction={state.runQuickAction}
        onBulkAction={state.runBulkAction}
      />

      <InboxReadingSection
        openItem={state.openItem}
        openMailbox={state.openMailbox}
        timeZone={state.timeZone}
        externalProvider={state.openItem ? state.externalProvider(state.openItem.mailbox_id) : null}
        roleName={
          state.openItem && state.openItem.role_id !== null
            ? (state.roleList.find(role => role.id === state.openItem?.role_id)?.name ?? null)
            : null
        }
        navigation={state.navigation}
        onUnavailable={state.refreshListing}
        actionsRef={state.paneActions}
        triageEnabled={state.triageCounts !== null}
        triageMenuOpen={state.triageMenuOpen}
        onTriageMenuOpenChange={state.setTriageMenuOpen}
        onUseTriageDraft={state.useTriageDraft}
      />

      {externalDialogs}

      <InboxOverlayDialogs
        scope={scope}
        organizationId={state.organizationId}
        timeZone={state.timeZone}
        canManage={canManage}
        mailboxes={state.mailboxes.data}
        roleList={state.roleList}
        sendable={state.sendable}
        sidebarSheet={state.sidebarSheet}
        sidebarOpen={state.sidebarOpen}
        onSidebarOpenChange={state.setSidebarOpen}
        sidebarProps={state.sidebarProps}
        onView={state.onView}
        onBox={state.onBox}
        onRole={state.onRole}
        compose={state.compose}
        helpMounted={state.helpMounted}
        helpOpen={state.helpOpen}
        onHelpOpenChange={state.setHelpOpen}
        paletteMounted={state.paletteMounted}
        paletteOpen={state.paletteOpen}
        onPaletteOpenChange={state.setPaletteOpen}
        restoreFocus={state.restoreFocus}
        runCommand={state.runCommand}
        managingMailboxes={state.managingMailboxes}
        newMailboxType={state.newMailboxType}
        onManagingMailboxesChange={open => {
          state.setManagingMailboxes(open);
          if (!open) state.setNewMailboxType(undefined);
        }}
        onRefreshMailboxes={() => void state.mailboxes.refetch()}
        managingRoles={state.managingRoles}
        onManagingRolesChange={state.setManagingRoles}
        draftsMounted={state.draftsMounted}
        draftsOpen={state.draftsOpen}
        onDraftsOpenChange={state.setDraftsOpen}
        onOpenSavedDraft={state.openSavedDraft}
        draft={state.draft}
        onCloseDraft={() => state.setDraft(null)}
        onReopenDraft={state.reopenDraft}
        claimPublicMounted={state.claimPublicMounted}
        claimPublicOpen={state.claimPublicOpen}
        onClaimPublicOpenChange={state.setClaimPublicOpen}
        publicMailboxOptions={state.publicMailboxOptions}
        onPublicMailboxClaimed={mailbox => {
          state.setClaimPublicOpen(false);
          state.onBox(mailbox.id);
        }}
      />
    </div>
  );
}
