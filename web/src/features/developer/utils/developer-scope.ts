import type { MailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import type { MailboxDomain } from '@/features/mailbox/types';

/**
 * Whether the caller has the developer tools: owners, admins, and members with developer access.
 * They write transactional templates, manage the suppression list, and read lane usage.
 */
export function canUseDeveloperTools(organization: MailOrganization): boolean {
  if (organization.canManage) return true;
  // A granted member always carries a scope; without one the API refuses developer writes.
  return organization.features.developer && Boolean(organization.developerScope);
}

/** Whether the caller may create and edit transactional templates at all. */
export const canWriteTransactional = canUseDeveloperTools;

/**
 * Whether a transactional template may send from the domain: owners and admins anywhere, a
 * developer only inside the grant's scope. The API decides; this keeps the console honest.
 */
export function developerScopeAllows(organization: MailOrganization, domain: string): boolean {
  if (organization.canManage) return true;
  const scope = organization.developerScope;
  if (!organization.features.developer || !scope) return false;
  if (scope.all_domains) return true;
  return scope.domains.includes(domain.trim().toLowerCase());
}

/** Whether the console offers the domain as a transactional sender to this caller. */
export function offersSenderDomain(
  organization: MailOrganization,
  domain: Pick<MailboxDomain, 'domain' | 'kind'>
): boolean {
  if (organization.canManage) return true;
  // The shared domain is never inside a developer's scope.
  return domain.kind === 'organization' && developerScopeAllows(organization, domain.domain);
}
