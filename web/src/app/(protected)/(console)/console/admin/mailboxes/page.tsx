import { SharedMailboxesAdmin } from '@/features/mailbox/components/admin/shared-mailboxes-admin';

export default function AdminMailboxesPage() {
  return (
    <div className="mx-auto w-full max-w-6xl px-4 py-6 sm:px-6 lg:py-8">
      <SharedMailboxesAdmin />
    </div>
  );
}
