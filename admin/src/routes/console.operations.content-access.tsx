import { createFileRoute, redirect } from '@tanstack/react-router';
import { z } from 'zod';
import { isOperatorFeatureEnabled } from '@/config/env';
import { ContentAccessPage } from '@/features/mail-operations/components/content-access-page';
import { daySearchField } from '@/features/mail-operations/search';

const searchSchema = z.object({
  organization_id: z.coerce.number().int().positive().optional().catch(undefined),
  viewer_user_id: z.coerce.number().int().positive().optional().catch(undefined),
  from: daySearchField,
  to: daySearchField,
  page: z.coerce.number().int().min(1).optional().catch(undefined),
});

export const Route = createFileRoute('/console/operations/content-access')({
  validateSearch: searchSchema,
  beforeLoad: () => {
    if (!isOperatorFeatureEnabled('mailbox') || !isOperatorFeatureEnabled('organization')) {
      throw redirect({ to: '/console', replace: true });
    }
  },
  component: function ContentAccessRoute() {
    const search = Route.useSearch();
    const navigate = Route.useNavigate();
    return (
      <ContentAccessPage
        search={search}
        onSearchChange={(next) => void navigate({ search: next, replace: true })}
      />
    );
  },
});
