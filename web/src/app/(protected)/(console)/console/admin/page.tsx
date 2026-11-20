import { redirect } from 'next/navigation';

import { ROUTES } from '@/constants/routes';

interface AdminPageProps {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}

/** The admin area opens on its shared mailbox page, keeping the chosen organization. */
export default async function AdminPage({ searchParams }: AdminPageProps) {
  const { org } = await searchParams;
  const query = typeof org === 'string' ? `?${new URLSearchParams({ org }).toString()}` : '';
  redirect(`${ROUTES.CONSOLE.ADMIN_MAILBOXES}${query}`);
}
