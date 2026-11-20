import type { z } from 'zod';
import {
  assertContract,
  type Accepts,
  type ContractData,
  type ContractResponse,
} from '@/http/contract';
import { http } from '@/http/client';
import {
  mailMetricsSchema,
  mailOrganizationPageSchema,
  outboundMessagePageSchema,
  type MailMetrics,
  type MailOrganizationPage,
  type MailOrganizationSearch,
  type OutboundMessagePage,
  type OutboundMessageSearch,
} from '@/features/mail-operations/types';

/** Rows per page, as the spec asks: twenty organizations or messages. */
export const mailOperationsPageSize = 20;

/** Days in the overview trend. */
export const mailTrendDays = 7;

function withSignal(signal?: AbortSignal) {
  return signal ? { signal } : {};
}

export const mailOperationsService = {
  overview(date: string | undefined, signal?: AbortSignal): Promise<MailMetrics> {
    const params = new URLSearchParams({ days: String(mailTrendDays) });
    if (date) {
      params.set('date', date);
    }
    return http.get(`/v1/operator/mail-metrics?${params.toString()}`, {
      schema: mailMetricsSchema,
      ...withSignal(signal),
    });
  },
  organizations(
    search: MailOrganizationSearch,
    signal?: AbortSignal,
  ): Promise<MailOrganizationPage> {
    const params = new URLSearchParams({
      page: String(search.page ?? 1),
      per_page: String(mailOperationsPageSize),
    });
    if (search.date) {
      params.set('date', search.date);
    }
    if (search.q) {
      params.set('q', search.q);
    }
    return http.get(`/v1/operator/mail-metrics/organizations?${params.toString()}`, {
      responseMode: 'json',
      schema: mailOrganizationPageSchema,
      ...withSignal(signal),
    });
  },
  outbound(search: OutboundMessageSearch, signal?: AbortSignal): Promise<OutboundMessagePage> {
    const params = new URLSearchParams({
      page: String(search.page ?? 1),
      per_page: String(mailOperationsPageSize),
    });
    if (search.date) {
      params.set('date', search.date);
    }
    if (search.organization_id) {
      params.set('organization_id', String(search.organization_id));
    }
    if (search.mailbox_id) {
      params.set('mailbox_id', String(search.mailbox_id));
    }
    if (search.status) {
      params.set('status', search.status);
    }
    return http.get(`/v1/operator/outbound-messages?${params.toString()}`, {
      responseMode: 'json',
      schema: outboundMessagePageSchema,
      ...withSignal(signal),
    });
  },
};

// Fails type-check when contracts/openapi.yaml allows a body these schemas would reject.
assertContract<
  Accepts<z.input<typeof mailMetricsSchema>, ContractData<'getOperatorMailMetrics', 200>>
>();
assertContract<
  Accepts<
    z.input<typeof mailOrganizationPageSchema>,
    ContractResponse<'listOperatorMailMetricsOrganizations', 200>
  >
>();
assertContract<
  Accepts<
    z.input<typeof outboundMessagePageSchema>,
    ContractResponse<'listOperatorOutboundMessages', 200>
  >
>();
