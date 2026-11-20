import type { PropsWithChildren } from 'react';
import { getMessages } from 'next-intl/server';

import { isWebFeatureEnabled } from '@/config/features';
import { developerMessageNamespaces } from '@/i18n/client-message-namespaces';
import { selectMessageNamespaces } from '@/i18n/message-selection';
import { RouteMessagesProvider } from '@/i18n/route-messages-provider';

export default async function DevelopersLayout({ children }: PropsWithChildren) {
  const messages = selectMessageNamespaces(
    await getMessages(),
    developerMessageNamespaces(isWebFeatureEnabled('webhook'))
  );
  return <RouteMessagesProvider additionalMessages={messages}>{children}</RouteMessagesProvider>;
}
