import { createFileRoute, redirect } from '@tanstack/react-router';
import { z } from 'zod';
import { isOperatorFeatureEnabled } from '@/config/env';
import { MailOrganizationsPage } from '@/features/mail-operations/components/mail-organizations-page';
import { daySearchField } from '@/features/mail-operations/search';

const searchSchema = z.object({
  date: daySearchField,
  q: z.string().trim().max(100).optional().catch(undefined),
  page: z.coerce.number().int().min(1).optional().catch(undefined),
});

export const Route = createFileRoute('/console/operations/organizations')({
  validateSearch: searchSchema,
  beforeLoad: () => {
    if (!isOperatorFeatureEnabled('mailbox') || !isOperatorFeatureEnabled('organization')) {
      throw redirect({ to: '/console', replace: true });
    }
  },
  component: function MailOrganizationsRoute() {
    const search = Route.useSearch();
    const navigate = Route.useNavigate();
    return (
      <MailOrganizationsPage
        search={search}
        onSearchChange={(next) => void navigate({ search: next, replace: true })}
        onBack={() => void navigate({ to: '/console/operations', search: { date: search.date } })}
        onOpenMembers={(organizationId) =>
          void navigate({
            to: '/console/organizations/$organizationId',
            params: { organizationId: String(organizationId) },
          })
        }
        onOpenMessages={(organizationId) =>
          void navigate({
            to: '/console/operations/messages',
            search: { date: search.date, organization_id: organizationId },
          })
        }
      />
    );
  },
});
