import type { PropsWithChildren } from 'react';
import { notFound } from 'next/navigation';
import { getMessages } from 'next-intl/server';

import { isWebFeatureEnabled } from '@/config/features';
import { AdminShell } from '@/features/mailbox/components/admin/admin-shell';
import { CLIENT_MESSAGE_NAMESPACES } from '@/i18n/client-message-namespaces';
import { selectMessageNamespaces } from '@/i18n/message-selection';
import { RouteMessagesProvider } from '@/i18n/route-messages-provider';

/** The organization admin area: needs the mailbox and organization features; owners and admins. */
export default async function AdminLayout({ children }: PropsWithChildren) {
  if (!isWebFeatureEnabled('mailbox') || !isWebFeatureEnabled('organization')) notFound();
  const messages = selectMessageNamespaces(await getMessages(), CLIENT_MESSAGE_NAMESPACES.mailbox);
  return (
    <RouteMessagesProvider additionalMessages={messages}>
      <AdminShell>{children}</AdminShell>
    </RouteMessagesProvider>
  );
}
