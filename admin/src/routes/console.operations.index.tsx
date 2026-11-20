import { createFileRoute, redirect } from '@tanstack/react-router';
import { z } from 'zod';
import { isOperatorFeatureEnabled } from '@/config/env';
import { MailOverviewPage } from '@/features/mail-operations/components/mail-overview-page';
import { daySearchField } from '@/features/mail-operations/search';

const searchSchema = z.object({ date: daySearchField });

export const Route = createFileRoute('/console/operations/')({
  validateSearch: searchSchema,
  beforeLoad: () => {
    if (!isOperatorFeatureEnabled('mailbox') || !isOperatorFeatureEnabled('organization')) {
      throw redirect({ to: '/console', replace: true });
    }
  },
  component: function MailOverviewRoute() {
    const search = Route.useSearch();
    const navigate = Route.useNavigate();
    return (
      <MailOverviewPage
        search={search}
        onSearchChange={(next) => void navigate({ search: next, replace: true })}
        onOpenDay={(date) =>
          void navigate({ to: '/console/operations/organizations', search: { date } })
        }
      />
    );
  },
});
