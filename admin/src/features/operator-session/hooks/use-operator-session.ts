import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useMemo } from 'react';
import { ApiErrorCode } from '@/http/codes';
import { ApiError, csrf } from '@/http/client';
import { operatorSessionService } from '@/features/operator-session/services/operator-session-service';
import { isFeatureEnabled } from '@/config/env';
import type {
  OperatorCapability,
  OperatorSession,
  SignInInput,
} from '@/features/operator-session/types';

export const operatorSessionQueryKey = ['operator', 'session'] as const;

export function operatorSessionQueryOptions() {
  return queryOptions({
    queryKey: operatorSessionQueryKey,
    queryFn: async ({ signal }) => {
      const session = await operatorSessionService.current(signal);
      csrf.set(session.csrf_token);
      return session;
    },
    retry: false,
    staleTime: 60_000,
  });
}

/** Errors that mean the browser has no usable operator session and must sign in again. */
export function isSignedOut(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    (error.status === 401 ||
      error.errorCode === ApiErrorCode.OPERATOR_FORBIDDEN ||
      error.errorCode === ApiErrorCode.AUTH_ACCOUNT_DISABLED)
  );
}

export function useOperatorSession() {
  return useQuery(operatorSessionQueryOptions());
}

export function useSignIn() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: SignInInput) => operatorSessionService.signIn(input),
    onSuccess: (session: OperatorSession) => {
      csrf.set(session.csrf_token);
      queryClient.setQueryData(operatorSessionQueryKey, session);
    },
  });
}

export function useSignOut() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => operatorSessionService.signOut(),
    onSettled: () => {
      csrf.set(undefined);
      queryClient.clear();
    },
  });
}

/** Refetches the session once so a stale CSRF token can be replaced before a retry. */
export async function recoverCsrfToken(): Promise<void> {
  const session = await operatorSessionService.current();
  csrf.set(session.csrf_token);
}

/**
 * Whether the signed-in operator's grant includes a capability. UX only: the API authorizes every
 * request. Builds without operator sign-in have no session and show every screen.
 */
export function useOperatorCapability(capability: OperatorCapability): boolean {
  const operatorEnabled = isFeatureEnabled('operator');
  const session = useQuery({ ...operatorSessionQueryOptions(), enabled: operatorEnabled });
  if (!operatorEnabled) {
    return true;
  }
  return session.data?.capabilities.includes(capability) ?? false;
}

/** The capabilities of the signed-in operator, or every capability when sign-in is not built in. */
export function useOperatorCapabilities(): ReadonlySet<OperatorCapability> {
  const manage = useOperatorCapability('operator:manage');
  const metrics = useOperatorCapability('mail_metrics:read');
  const content = useOperatorCapability('mail_content:read');
  return useMemo(() => {
    const granted = new Set<OperatorCapability>();
    if (manage) {
      granted.add('operator:manage');
    }
    if (metrics) {
      granted.add('mail_metrics:read');
    }
    if (content) {
      granted.add('mail_content:read');
    }
    return granted;
  }, [manage, metrics, content]);
}
