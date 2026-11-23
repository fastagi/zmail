import { maxOrganizationDailySendLimit } from '@/features/organizations/types';

/** Parses the limit field: empty is the instance default, otherwise a whole number in range. */
export function parseSendLimit(value: string): number | null | undefined {
  const trimmed = value.trim();
  if (trimmed === '') {
    return null;
  }
  if (!/^\d{1,7}$/.test(trimmed)) {
    return undefined;
  }
  const limit = Number(trimmed);
  return limit >= 1 && limit <= maxOrganizationDailySendLimit ? limit : undefined;
}
