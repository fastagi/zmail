'use client';

import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from 'react';
import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { AlertCircle, Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { ROUTES } from '@/constants/routes';
import type { ExternalMailCompletion } from '@/features/mailbox/external-mail-schemas';
import {
  useBeginExternalAuthorization,
  useCompleteExternalMailAuthorization,
} from '@/features/mailbox/hooks/use-external-mail-actions';
import { MAIL_ORGANIZATION_PARAM } from '@/features/mailbox/components/mailbox-params';
import {
  EXTERNAL_MAIL_CALLBACK_PATH,
  forgetAuthorization,
  recallAuthorization,
  type PendingExternalAuthorization,
} from '@/features/mailbox/utils/external-mail';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';

type FailureKey =
  | 'notFound'
  | 'mismatch'
  | 'taken'
  | 'limit'
  | 'failed'
  | 'denied'
  | 'unavailable'
  | 'missing'
  | 'generic';

type CallbackState =
  | { status: 'working' }
  | { status: 'failed'; reason: FailureKey; pending: PendingExternalAuthorization | null };

/** The plain-language reason for a refused completion. */
function failureOf(error: unknown): FailureKey {
  if (!(error instanceof ApiError)) return 'generic';
  switch (error.errorCode) {
    case ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_NOT_FOUND:
      return 'notFound';
    case ApiErrorCode.MAILBOX_EXTERNAL_ACCOUNT_MISMATCH:
      return 'mismatch';
    case ApiErrorCode.MAILBOX_EXTERNAL_ACCOUNT_TAKEN:
      return 'taken';
    case ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_LIMIT:
      return 'limit';
    case ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_FAILED:
      return 'failed';
    case ApiErrorCode.MAILBOX_EXTERNAL_PROVIDER_UNAVAILABLE:
      return 'unavailable';
    default:
      return 'generic';
  }
}

/**
 * States already submitted in this page. React's strict mode mounts the page twice in development,
 * and the API uses a state up on the first call, so a second call would always fail.
 */
const submittedStates = new Set<string>();

/** The inbox narrowed to one mailbox, in the organization the sign-in started from. */
function inboxHref(organizationId: number, mailboxId?: number): string {
  const params = new URLSearchParams({ [MAIL_ORGANIZATION_PARAM]: String(organizationId) });
  if (mailboxId !== undefined) params.set('box', String(mailboxId));
  return `${ROUTES.CONSOLE.MAILBOXES}?${params.toString()}`;
}

/**
 * The page the provider sends the browser back to (roadmap §5.1, item 3). It reads `state` and
 * `code` (or the provider's `error`), finds the organization the sign-in started from in session
 * storage under that `state`, finishes the sign-in once, and then opens the inbox on the mailbox:
 * `201` says it is syncing the chosen days, `200` that it reconnected. A refusal is explained in
 * plain language with a way to try another account.
 */
export function ExternalMailCallback() {
  const searchParams = useSearchParams();
  // Read once, then taken out of the address bar before anything else happens on every path, so
  // the single-use `state` and the `code` never stay in the history, a bookmark, or analytics.
  const [{ state, code, providerError }] = useState(() => ({
    state: searchParams.get('state') ?? '',
    code: searchParams.get('code') ?? '',
    providerError: searchParams.get('error'),
  }));
  useLayoutEffect(() => {
    if (window.location.search === '' && window.location.hash === '') return;
    window.history.replaceState(window.history.state, '', EXTERNAL_MAIL_CALLBACK_PATH);
  }, []);
  // Session storage exists only in the browser: the server and the hydrating render show the
  // working state, and the sign-in is read once hydrated.
  const hydrated = useSyncExternalStore(
    subscribeToNothing,
    () => true,
    () => false
  );
  if (!hydrated) return <Working />;
  return (
    <PendingCallback key={state} state={state} code={code} providerError={providerError} />
  );
}

const subscribeToNothing = () => () => {};

function PendingCallback({
  state,
  code,
  providerError,
}: {
  state: string;
  code: string;
  providerError: string | null;
}) {
  // Read once: the entry is forgotten as soon as the completion is sent.
  const [pending] = useState(() => (state ? recallAuthorization(state) : null));
  return <CallbackBody state={state} code={code} providerError={providerError} pending={pending} />;
}

function Working() {
  const t = useT('mailbox');
  return (
    <div className="flex flex-col items-center gap-3 text-center" role="status">
      <Loader2 className="size-6 animate-spin text-text-muted motion-reduce:animate-none" />
      <p className="text-sm text-text-muted">{t('external.callback.working')}</p>
    </div>
  );
}

function CallbackBody({
  state,
  code,
  providerError,
  pending,
}: {
  state: string;
  code: string;
  providerError: string | null;
  pending: PendingExternalAuthorization | null;
}) {
  const t = useT('mailbox');
  const router = useRouter();
  // Hooks need an organization; 0 disables nothing here because they only run on demand.
  const organizationId = pending?.organizationId ?? 0;
  const complete = useCompleteExternalMailAuthorization(organizationId);
  const retry = useBeginExternalAuthorization(organizationId);
  const started = useRef(false);

  let initial: CallbackState = { status: 'working' };
  if (providerError !== null) {
    initial = {
      status: 'failed',
      reason: providerError === 'access_denied' ? 'denied' : 'failed',
      pending,
    };
  } else if (!state || !code) {
    initial = { status: 'failed', reason: 'missing', pending };
  } else if (!pending) {
    initial = { status: 'failed', reason: 'notFound', pending: null };
  }
  const [view, setView] = useState<CallbackState>(initial);

  const { mutate } = complete;
  useEffect(() => {
    if (state) forgetAuthorization(state);
    if (started.current || initial.status !== 'working' || !pending) return;
    if (submittedStates.has(state)) return;
    started.current = true;
    submittedStates.add(state);
    mutate(
      { state, code },
      {
        onSuccess: ({ outcome, connection }: ExternalMailCompletion) => {
          if (outcome === 'created') {
            toast.success(
              pending.backfillDays
                ? t('external.callback.connected', { days: pending.backfillDays })
                : t('external.callback.connectedRecent')
            );
          } else {
            toast.success(t('external.callback.reconnected'));
          }
          router.replace(inboxHref(pending.organizationId, connection.mailbox_id));
        },
        onError: error => setView({ status: 'failed', reason: failureOf(error), pending }),
      }
    );
    // The completion runs once per state; later renders never send it again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);

  if (view.status === 'working') return <Working />;

  const again = view.pending;
  return (
    <div className="space-y-4">
      <Alert variant="destructive">
        <AlertCircle className="size-4" aria-hidden="true" />
        <AlertDescription>{t(`external.callback.errors.${view.reason}`)}</AlertDescription>
      </Alert>
      {retry.error ? (
        <p role="alert" className="text-xs text-error">
          {t('external.connect.failed')}
        </p>
      ) : null}
      <div className="flex flex-wrap gap-2">
        {again ? (
          <Button
            type="button"
            loading={retry.isPending}
            onClick={() =>
              retry.begin(
                again.connectionId !== null
                  ? { kind: 'reconnect', provider: again.provider, connectionId: again.connectionId }
                  : {
                      kind: 'new',
                      provider: again.provider,
                      backfillDays: again.backfillDays ?? 14,
                    }
              )
            }
          >
            {t('external.callback.tryAnother')}
          </Button>
        ) : null}
        <Button asChild variant={again ? 'outline' : 'default'}>
          <Link href={again ? inboxHref(again.organizationId) : ROUTES.CONSOLE.MAILBOXES}>
            {t('external.callback.backToInbox')}
          </Link>
        </Button>
      </div>
    </div>
  );
}

/** Test seam: forgets which states this page already submitted. */
export function resetSubmittedStates(): void {
  submittedStates.clear();
}
