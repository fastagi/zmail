import { createFileRoute, redirect } from '@tanstack/react-router';
import { z } from 'zod';
import { isOperatorFeatureEnabled } from '@/config/env';
import { OutboundMessagesPage } from '@/features/mail-operations/components/outbound-messages-page';
import { daySearchField } from '@/features/mail-operations/search';
import { outboundStatuses } from '@/features/mail-operations/types';

const searchSchema = z.object({
  date: daySearchField,
  organization_id: z.coerce.number().int().positive().optional().catch(undefined),
  mailbox_id: z.coerce.number().int().positive().optional().catch(undefined),
  status: z.enum(outboundStatuses).optional().catch(undefined),
  page: z.coerce.number().int().min(1).optional().catch(undefined),
});

export const Route = createFileRoute('/console/operations/messages')({
  validateSearch: searchSchema,
  beforeLoad: () => {
    if (!isOperatorFeatureEnabled('mailbox') || !isOperatorFeatureEnabled('organization')) {
      throw redirect({ to: '/console', replace: true });
    }
  },
  component: function OutboundMessagesRoute() {
    const search = Route.useSearch();
    const navigate = Route.useNavigate();
    return (
      <OutboundMessagesPage
        search={search}
        onSearchChange={(next) => void navigate({ search: next, replace: true })}
        onBack={() =>
          void navigate({ to: '/console/operations/organizations', search: { date: search.date } })
        }
      />
    );
  },
});
