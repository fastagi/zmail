/** Session key of the organization the console last acted in; shared by the switcher and mail. */
export const ACTIVE_ORGANIZATION_STORAGE_KEY = 'zmail.activeOrganizationId';

/** Forgets the remembered active organization when it is the given one. */
export function forgetActiveOrganization(organizationId: number): void {
  try {
    if (window.sessionStorage.getItem(ACTIVE_ORGANIZATION_STORAGE_KEY) === String(organizationId)) {
      window.sessionStorage.removeItem(ACTIVE_ORGANIZATION_STORAGE_KEY);
    }
  } catch {
    // Storage may be unavailable in restricted contexts; the organization list stays authoritative.
  }
}
