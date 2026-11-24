'use client';

import { lazy, Suspense, useEffect, useState, type RefObject } from 'react';
import { Settings2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import type { PaletteCommand } from '@/features/mailbox/components/command-palette';
import type { ComposeDraft } from '@/features/mailbox/components/compose-drafts';
import type { ExternalConnectPreset } from '@/features/mailbox/components/external-connect-dialog';
import type { ReadyScope } from '@/features/mailbox/components/inbox/inbox-url';
import { InboxListHeaderSkeleton, InboxListSkeleton } from '@/features/mailbox/components/inbox-list';
import {
  InboxSidebar,
  visibleRoles,
  type InboxRoleFilter,
  type InboxSidebarExternal,
} from '@/features/mailbox/components/inbox-sidebar';
import {
  MailOrganizationName,
  MailPageHeader,
  mailPageClassName,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import { ReaderArticleSkeleton } from '@/features/mailbox/components/mail-shared';
import type {
  ExternalMailConnection,
  ExternalMailConnector,
} from '@/features/mailbox/external-mail-schemas';
import type { MailDraftSummary } from '@/features/mailbox/draft-schemas';
import { useMailShortcuts, type MailShortcut } from '@/features/mailbox/hooks/use-mail-shortcuts';
import { useMailboxDomains } from '@/features/mailbox/hooks/use-mailbox';
import type { InboxCounts, InboxView, MailRole, Mailbox } from '@/features/mailbox/types';
import { canComposeFrom } from '@/features/mailbox/utils/connected-mailbox';
import { useT } from '@/i18n';
import { cn } from '@/utils';

// Administration dialogs load when opened, not with the mail page.
const DraftsDialog = lazy(() => import('@/features/mailbox/components/drafts-dialog'));
const ManageRolesDialog = lazy(async () => {
  const feature = await import('@/features/mailbox/components/manage-roles-dialog');
  return { default: feature.ManageRolesDialog };
});
const ManageMailboxesDialog = lazy(async () => {
  const feature = await import('@/features/mailbox/components/manage-mailboxes-dialog');
  return { default: feature.ManageMailboxesDialog };
});
const ShortcutHelpDialog = lazy(async () => ({
  default: (await import('@/features/mailbox/components/shortcut-help-dialog')).ShortcutHelpDialog,
}));
// The composer loads when it first opens.
export const ComposeDialog = lazy(async () => ({
  default: (await import('@/features/mailbox/components/compose-dialog')).ComposeDialog,
}));
const CommandPalette = lazy(async () => ({
  default: (await import('@/features/mailbox/components/command-palette')).CommandPalette,
}));
// The sidebar sheet of narrow screens loads when first opened.
const Sheet = lazy(async () => ({ default: (await import('@/components/ui/sheet')).Sheet }));
const SheetContent = lazy(async () => ({
  default: (await import('@/components/ui/sheet')).SheetContent,
}));
const SheetTitle = lazy(async () => ({
  default: (await import('@/components/ui/sheet')).SheetTitle,
}));
// External mailboxes: the connect and manage dialogs load when opened.
const ExternalConnectDialog = lazy(async () => ({
  default: (await import('@/features/mailbox/components/external-connect-dialog'))
    .ExternalConnectDialog,
}));
const ConnectedMailboxesDialog = lazy(async () => ({
  default: (await import('@/features/mailbox/components/connected-mailboxes-dialog'))
    .ConnectedMailboxesDialog,
}));
const MailboxAccessPanel = lazy(async () => {
  const feature = await import('@/features/mailbox/components/mailbox-access-panel');
  return { default: feature.MailboxAccessPanel };
});

export interface ActiveDraft {
  mailbox: Mailbox;
  draft: ComposeDraft;
  /** The mailboxes a new message may be sent from; replies keep their own. */
  fromOptions?: readonly Mailbox[];
}

export interface InboxSidebarSharedProps {
  external: InboxSidebarExternal;
  sort: string;
  onSort: (value: string) => void;
  counts: InboxCounts | undefined;
  mailboxes: Mailbox[];
  roles: MailRole[] | undefined;
  canManage: boolean;
  view: InboxView;
  box: number | null;
  role: InboxRoleFilter | null;
  onManageRoles: () => void;
  onShortcuts: () => void;
  drafts: { count: number | undefined; onOpen: () => void } | null;
}

/** A state of the page with nothing to list, in the console's ordinary frame and header. */
export function InboxFrame({ scope, children }: { scope: ReadyScope; children: React.ReactNode }) {
  const t = useT('mailbox');
  return (
    <div className={cn(mailPageClassName, 'space-y-5')}>
      <MailPageHeader title={t('inbox.title')} description={t('inbox.description')} scope={scope} />
      {children}
    </div>
  );
}

/** The inbox keys, bound while the three panes are on screen. */
export function MailShortcuts({
  bindings,
}: {
  bindings: Partial<Record<MailShortcut, () => void>>;
}) {
  useMailShortcuts(bindings, { enabled: true });
  return null;
}

export function ExternalMailboxDialogs({
  organizationId,
  connectMounted,
  connectSession,
  connectOpen,
  onConnectOpenChange,
  connectorList,
  connectPreset,
  manageExternalMounted,
  manageExternalOpen,
  onManageExternalOpenChange,
  connections,
  connectors,
  connectionsError,
  externalOn,
  onOpenConnect,
  returnFocusRef,
}: {
  organizationId: number;
  connectMounted: boolean;
  connectSession: number;
  connectOpen: boolean;
  onConnectOpenChange: (open: boolean) => void;
  connectorList: ExternalMailConnector[];
  connectPreset: ExternalConnectPreset | null;
  manageExternalMounted: boolean;
  manageExternalOpen: boolean;
  onManageExternalOpenChange: (open: boolean) => void;
  connections: ExternalMailConnection[] | undefined;
  connectors: ExternalMailConnector[] | undefined;
  connectionsError: boolean;
  externalOn: boolean;
  onOpenConnect: (preset?: ExternalConnectPreset | null) => void;
  returnFocusRef: RefObject<HTMLElement | null>;
}) {
  return (
    <>
      {connectMounted ? (
        <Suspense fallback={null}>
          <ExternalConnectDialog
            key={connectSession}
            open={connectOpen}
            onOpenChange={onConnectOpenChange}
            organizationId={organizationId}
            connectors={connectorList}
            preset={connectPreset}
            returnFocusRef={returnFocusRef}
          />
        </Suspense>
      ) : null}
      {manageExternalMounted ? (
        <Suspense fallback={null}>
          <ConnectedMailboxesDialog
            open={manageExternalOpen}
            onOpenChange={onManageExternalOpenChange}
            organizationId={organizationId}
            connections={connections}
            connectors={connectors}
            error={connectionsError}
            onAdd={externalOn ? () => onOpenConnect() : null}
            onReconnectWithPassword={onOpenConnect}
            returnFocusRef={returnFocusRef}
          />
        </Suspense>
      ) : null}
    </>
  );
}

export function InboxOverlayDialogs({
  scope,
  organizationId,
  timeZone,
  canManage,
  mailboxes,
  roleList,
  sendable,
  sidebarSheet,
  sidebarOpen,
  onSidebarOpenChange,
  sidebarProps,
  onView,
  onBox,
  onRole,
  compose,
  helpMounted,
  helpOpen,
  onHelpOpenChange,
  paletteMounted,
  paletteOpen,
  onPaletteOpenChange,
  restoreFocus,
  runCommand,
  managingMailboxes,
  newMailboxType,
  onManagingMailboxesChange,
  onRefreshMailboxes,
  managingRoles,
  onManagingRolesChange,
  draftsMounted,
  draftsOpen,
  onDraftsOpenChange,
  onOpenSavedDraft,
  draft,
  onCloseDraft,
  onReopenDraft,
}: {
  scope: ReadyScope;
  organizationId: number;
  timeZone: string;
  canManage: boolean;
  mailboxes: Mailbox[];
  roleList: MailRole[];
  sendable: Mailbox[];
  sidebarSheet: boolean;
  sidebarOpen: boolean;
  onSidebarOpenChange: (open: boolean) => void;
  sidebarProps: InboxSidebarSharedProps;
  onView: (view: InboxView) => void;
  onBox: (mailboxId: number) => void;
  onRole: (role: InboxRoleFilter) => void;
  compose: (() => void) | null;
  helpMounted: boolean;
  helpOpen: boolean;
  onHelpOpenChange: (open: boolean) => void;
  paletteMounted: boolean;
  paletteOpen: boolean;
  onPaletteOpenChange: (open: boolean) => void;
  restoreFocus: (event: Event) => void;
  runCommand: (command: PaletteCommand) => void;
  managingMailboxes: boolean;
  newMailboxType: 'personal' | undefined;
  onManagingMailboxesChange: (open: boolean) => void;
  onRefreshMailboxes: () => void;
  managingRoles: boolean;
  onManagingRolesChange: (open: boolean) => void;
  draftsMounted: boolean;
  draftsOpen: boolean;
  onDraftsOpenChange: (open: boolean) => void;
  onOpenSavedDraft: (summary: MailDraftSummary) => void;
  draft: ActiveDraft | null;
  onCloseDraft: () => void;
  onReopenDraft: (value: ActiveDraft) => () => void;
}) {
  const t = useT('mailbox');
  /** In the sheet, every choice also closes it. */
  const closing =
    <A extends unknown[]>(action: (...args: A) => void) =>
    (...args: A) => {
      onSidebarOpenChange(false);
      action(...args);
    };

  return (
    <>
      {sidebarSheet ? (
        <Suspense fallback={null}>
          <Sheet open={sidebarOpen} onOpenChange={onSidebarOpenChange}>
            <SheetContent
              side="left"
              closeLabel={t('actions.close')}
              className="w-[280px] gap-0 overflow-y-auto bg-bg-canvas px-3 pt-12 pb-3.5 motion-reduce:data-[state=closed]:animate-none motion-reduce:data-[state=open]:animate-none"
            >
              <SheetTitle className="sr-only">{t('inbox.groups.label')}</SheetTitle>
              <InboxSidebar
                {...sidebarProps}
                organization={<MailOrganizationName scope={scope} compact />}
                onView={closing(onView)}
                onBox={closing(onBox)}
                onRole={closing(onRole)}
                onCompose={compose ? closing(compose) : null}
              />
            </SheetContent>
          </Sheet>
        </Suspense>
      ) : null}

      {helpMounted ? (
        <Suspense fallback={null}>
          <ShortcutHelpDialog
            open={helpOpen}
            onOpenChange={onHelpOpenChange}
            onCloseAutoFocus={restoreFocus}
          />
        </Suspense>
      ) : null}

      {paletteMounted ? (
        <Suspense fallback={null}>
          <CommandPalette
            open={paletteOpen}
            onOpenChange={onPaletteOpenChange}
            onCloseAutoFocus={restoreFocus}
            organizationId={organizationId}
            timeZone={timeZone}
            mailboxes={mailboxes}
            roles={visibleRoles(mailboxes, roleList, canManage)}
            canManage={canManage}
            canCompose={compose !== null}
            onCommand={runCommand}
          />
        </Suspense>
      ) : null}

      {canManage ? (
        <ManageMailboxesFlow
          open={managingMailboxes}
          initialType={newMailboxType}
          onOpenChange={onManagingMailboxesChange}
          mailboxes={mailboxes}
          onRefresh={onRefreshMailboxes}
        />
      ) : null}

      {canManage && managingRoles ? (
        <Suspense fallback={null}>
          <ManageRolesDialog open onOpenChange={onManagingRolesChange} />
        </Suspense>
      ) : null}

      {draftsMounted ? (
        <Suspense fallback={null}>
          <DraftsDialog
            organizationId={organizationId}
            mailboxes={sendable}
            open={draftsOpen}
            onOpenChange={onDraftsOpenChange}
            onOpenDraft={onOpenSavedDraft}
          />
        </Suspense>
      ) : null}

      {draft && canComposeFrom(draft.mailbox) ? (
        <Suspense fallback={null}>
          <ComposeDialog
            mailbox={draft.mailbox}
            fromOptions={draft.fromOptions}
            draft={draft.draft}
            onClose={onCloseDraft}
            onStored={onCloseDraft}
            onReopen={onReopenDraft(draft)}
          />
        </Suspense>
      ) : null}
    </>
  );
}

/**
 * Manage mailboxes for an owner or admin with nothing to read: create a mailbox or open one's
 * access to add themselves. The domains load only when this is shown.
 */
export function ManageMailboxesAction({
  mailboxes,
  onRefresh,
  autoOpen = false,
  onAutoOpened,
}: {
  mailboxes: Mailbox[];
  onRefresh: () => void;
  /** Opens the dialog as soon as the domains are known: the team was just created. */
  autoOpen?: boolean;
  onAutoOpened?: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const domains = useMailboxDomains(organizationId);
  const [open, setOpen] = useState(false);
  const domainsReady = domains.data !== undefined;
  useEffect(() => {
    if (!autoOpen || !domainsReady) return;
    setOpen(true);
    onAutoOpened?.();
  }, [autoOpen, domainsReady, onAutoOpened]);

  return (
    <>
      <Button
        type="button"
        size="sm"
        variant="outline"
        icon={<Settings2 className="size-4" />}
        disabled={!domains.data}
        onClick={() => setOpen(true)}
      >
        {t('switcher.manage')}
      </Button>
      <ManageMailboxesFlow
        open={open}
        onOpenChange={setOpen}
        mailboxes={mailboxes}
        onRefresh={onRefresh}
      />
    </>
  );
}

/** Manage mailboxes, then the access panel of the mailbox it opens; each loads when first shown. */
export function ManageMailboxesFlow({
  open,
  onOpenChange,
  mailboxes,
  onRefresh,
  initialType,
}: {
  open: boolean;
  initialType?: 'personal';
  onOpenChange: (open: boolean) => void;
  mailboxes: Mailbox[];
  onRefresh: () => void;
}) {
  const organizationId = useMailOrganizationId();
  const domains = useMailboxDomains(organizationId);
  const [accessTargetId, setAccessTargetId] = useState<number | null>(null);
  const domainList = domains.data ?? [];
  const domain = domainList.find(item => item.kind === 'shared') ?? domainList[0] ?? null;
  const accessTarget = mailboxes.find(item => item.id === accessTargetId) ?? null;

  return (
    <Suspense fallback={null}>
      {open && domains.data ? (
        <ManageMailboxesDialog
          open
          mailboxes={mailboxes}
          domain={domain}
          domains={domainList}
          initialType={initialType}
          onClose={() => onOpenChange(false)}
          onCreated={() => onOpenChange(false)}
          onOpenAccess={mailbox => {
            onOpenChange(false);
            setAccessTargetId(mailbox.id);
          }}
        />
      ) : null}
      {accessTarget ? (
        <MailboxAccessPanel
          mailbox={accessTarget}
          onClose={() => setAccessTargetId(null)}
          onRefresh={onRefresh}
        />
      ) : null}
    </Suspense>
  );
}

/** The reading pane while its code loads: the 52px bar over the article column. */
export function ReaderSkeleton() {
  return (
    <div className="flex h-full min-h-0 flex-col" aria-hidden="true">
      <div className="h-[52px] shrink-0 border-b border-border-subtle" />
      <div className="py-[34px]">
        <ReaderArticleSkeleton className="mx-auto w-full max-w-[640px] space-y-4 px-5 sm:px-7" />
      </div>
    </div>
  );
}

/** The three panes while the mailboxes load, at their finished sizes. */
export function InboxSkeleton() {
  return (
    <div className="flex h-full min-h-0 bg-bg-canvas" aria-hidden="true" data-inbox-skeleton="">
      <div className="hidden w-14 flex-none flex-col gap-2 px-2 pt-[18px] md:flex lg:w-[248px] lg:px-3">
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
      <div className="my-2.5 ml-2.5 flex-1 overflow-hidden rounded-[14px] border border-border-main bg-bg-surface max-lg:mr-2.5 md:ml-0 lg:w-[380px] lg:flex-none lg:rounded-r-none lg:border-y-0 xl:w-[470px]">
        <InboxListHeaderSkeleton />
        <InboxListSkeleton />
      </div>
      <div className="my-2.5 mr-2.5 hidden flex-1 rounded-r-[14px] border border-l-0 border-border-main bg-bg-surface lg:block" />
    </div>
  );
}
