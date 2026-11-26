'use client';

import { useContext, useEffect, useId, useRef, useState, useSyncExternalStore } from 'react';
import { useRouter } from 'next/navigation';
import {
  AlertCircle,
  CheckCircle2,
  Loader2,
  Mail,
  Sparkles,
} from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { ROUTES } from '@/constants/routes';
import { AuthStoreContext } from '@/features/auth/store/auth-store';
import type { AuthUser } from '@/features/auth/types';
import { CopyAddressButton } from '@/features/mailbox/components/copy-address-button';
import {
  useClaimPublicMailbox,
  usePublicMailboxAvailability,
} from '@/features/mailbox/hooks/use-public-mailbox';
import { MAILBOX_LOCAL_PART_MAX_LENGTH } from '@/features/mailbox/schemas';
import {
  extractSuggestedLocalPart,
  suggestHandleFromUser,
  type PublicMailboxOptions,
} from '@/features/mailbox/services/public-mailbox-service';
import { useWorkspaceModeStore } from '@/features/mailbox/stores/workspace-mode';
import type { Mailbox } from '@/features/mailbox/types';
import { useCreateOrganization } from '@/features/organization/hooks/use-organizations';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const noopSubscribe = () => () => {};

function useOptionalCurrentUser(): AuthUser | null {
  const store = useContext(AuthStoreContext);
  return useSyncExternalStore(
    store ? store.subscribe : noopSubscribe,
    () => store?.getState().user ?? null,
    () => store?.getState().user ?? null
  );
}

type ClaimErrorKey =
  | 'publicMailbox.errorTaken'
  | 'publicMailbox.errorReserved'
  | 'publicMailbox.errorInvalid'
  | 'publicMailbox.errorLimitReached'
  | 'publicMailbox.errorGeneric';

function resolveClaimErrorKey(error: unknown): ClaimErrorKey {
  if (!(error instanceof ApiError)) return 'publicMailbox.errorGeneric';
  switch (error.errorCode) {
    case ApiErrorCode.MAILBOX_ADDRESS_TAKEN:
      return 'publicMailbox.errorTaken';
    case ApiErrorCode.MAILBOX_ADDRESS_RESERVED:
      return 'publicMailbox.errorReserved';
    case ApiErrorCode.MAILBOX_ADDRESS_INVALID:
    case ApiErrorCode.COMMON_VALIDATION_FAILED:
    case ApiErrorCode.COMMON_INVALID_INPUT:
      return 'publicMailbox.errorInvalid';
    case ApiErrorCode.MAILBOX_LIMIT_REACHED:
      return 'publicMailbox.errorLimitReached';
    default:
      return 'publicMailbox.errorGeneric';
  }
}

export interface ClaimPublicMailboxCardProps {
  options: PublicMailboxOptions;
  /** Existing organization id when claiming inside the inbox; omit during onboarding. */
  organizationId?: number;
  /** Visual framing: `hero` inside TeamSetupCard, `card` standalone, `inline` inside a dialog. */
  variant?: 'hero' | 'card' | 'inline';
  /** Overrides default handle derivation (mostly for tests). */
  initialHandle?: string;
  /** Debounce duration for availability checks (defaults to 300ms). */
  debounceMs?: number;
  /** Called after claiming succeeds with the newly created mailbox and owning org id. */
  onClaimed?: (mailbox: Mailbox, organizationId: number) => void;
  /** Optional fallback action (e.g. enter Personal Mode without claiming yet). */
  onSkip?: () => void;
  skipPending?: boolean;
  className?: string;
}

export function ClaimPublicMailboxCard({
  options,
  organizationId,
  variant = 'card',
  initialHandle,
  debounceMs,
  onClaimed,
  onSkip,
  skipPending = false,
  className,
}: ClaimPublicMailboxCardProps) {
  const t = useT('organization');
  const router = useRouter();
  const inputId = useId();
  const domainId = useId();
  const inputRef = useRef<HTMLInputElement | null>(null);

  const currentUser = useOptionalCurrentUser();
  const setMode = useWorkspaceModeStore(state => state.setMode);
  const createOrganization = useCreateOrganization();
  const claimMutation = useClaimPublicMailbox();

  const defaultDomain = options.domains[0] ?? 'zmail.im';
  const [selectedDomain, setSelectedDomain] = useState(defaultDomain);
  const activeDomain = options.domains.includes(selectedDomain) ? selectedDomain : defaultDomain;

  const derivedHandle = initialHandle ?? suggestHandleFromUser(currentUser);
  const [localPart, setLocalPart] = useState(derivedHandle);
  const [touched, setTouched] = useState(Boolean(initialHandle));
  const [createdOrgId, setCreatedOrgId] = useState<number | null>(null);
  const [submitError, setSubmitError] = useState<unknown>(null);

  // Fill from auth user once if user resolves asynchronously before any manual edit.
  useEffect(() => {
    if (!touched && derivedHandle && localPart === '') {
      setLocalPart(derivedHandle);
    }
  }, [derivedHandle, localPart, touched]);

  const isAlreadyClaimed = options.claimed_count >= options.limit_per_user;
  const trimmedLocalPart = localPart.trim().toLowerCase();

  const availability = usePublicMailboxAvailability(trimmedLocalPart, activeDomain, {
    enabled: options.enabled && !isAlreadyClaimed && trimmedLocalPart.length > 0,
    debounceMs,
  });

  const isSubmitting = createOrganization.isPending || claimMutation.isPending;
  const availabilityData =
    !availability.isDebouncing && availability.data?.local_part === trimmedLocalPart
      ? availability.data
      : undefined;
  const status = availabilityData?.status;
  const isChecking =
    trimmedLocalPart.length > 0 && (availability.isDebouncing || availability.isFetching);
  const isUnavailableStatus =
    status === 'taken' || status === 'reserved' || status === 'too_short' || status === 'invalid';
  const canSubmit =
    !isAlreadyClaimed &&
    !isSubmitting &&
    trimmedLocalPart.length > 0 &&
    !isChecking &&
    !isUnavailableStatus;

  const handleSuggestionPick = (suggestion: string) => {
    const nextHandle = extractSuggestedLocalPart(suggestion);
    setTouched(true);
    setLocalPart(nextHandle);
    setSubmitError(null);
    claimMutation.reset();
    inputRef.current?.focus();
  };

  const handleSubmit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!canSubmit) return;
    setSubmitError(null);

    try {
      let targetOrgId = organizationId ?? createdOrgId;
      if (targetOrgId === null) {
        const org = await createOrganization.mutateAsync({ name: 'Personal' });
        targetOrgId = org.id;
        setCreatedOrgId(org.id);
      }

      const displayName = currentUser?.name?.trim() || undefined;
      const mailbox = await claimMutation.mutateAsync({
        organizationId: targetOrgId,
        input: {
          local_part: trimmedLocalPart,
          domain: activeDomain,
          ...(displayName ? { display_name: displayName } : {}),
        },
      });

      setMode('personal');
      toast.success(t('publicMailbox.claimedSuccess', { address: mailbox.address }));

      if (onClaimed) {
        onClaimed(mailbox, targetOrgId);
      } else {
        router.push(`${ROUTES.CONSOLE.MAILBOXES}?org=${targetOrgId}`);
      }
    } catch (error) {
      setSubmitError(error);
    }
  };

  const containerClass =
    variant === 'hero'
      ? 'rounded-lg border border-primary/25 bg-primary/5 p-4 sm:p-5'
      : variant === 'card'
        ? 'rounded-xl border border-primary/20 bg-card p-5 text-left shadow-xs sm:p-6'
        : 'text-left';

  if (isAlreadyClaimed) {
    const claimedAddress = options.claimed_address ?? '';
    return (
      <div className={cn(containerClass, className)} data-slot="claim-public-mailbox-claimed">
        <div className="flex items-start gap-3">
          <span className="flex size-10 shrink-0 items-center justify-center rounded-md bg-emerald-500/15 text-emerald-600 dark:text-emerald-400">
            <CheckCircle2 className="size-5" aria-hidden="true" />
          </span>
          <div className="min-w-0 flex-1 space-y-2">
            <div className="flex flex-wrap items-center gap-2">
              <p className="text-sm font-semibold text-foreground">
                {t('publicMailbox.alreadyClaimed', { address: claimedAddress })}
              </p>
              {claimedAddress ? (
                <CopyAddressButton
                  address={claimedAddress}
                  label={t('publicMailbox.copyAddress', { address: claimedAddress })}
                  copiedLabel={t('publicMailbox.addressCopied', { address: claimedAddress })}
                  failedLabel={t('publicMailbox.copyAddressFailed')}
                  size="xs"
                />
              ) : null}
            </div>
            {!organizationId && options.claimed_organization_id ? (
              <div>
                <Button
                  type="button"
                  size="sm"
                  onClick={() => {
                    setMode('personal');
                    router.push(
                      `${ROUTES.CONSOLE.MAILBOXES}?org=${options.claimed_organization_id}`
                    );
                  }}
                >
                  {t('publicMailbox.openInbox')}
                </Button>
              </div>
            ) : null}
          </div>
        </div>
      </div>
    );
  }

  const displayError = submitError ?? claimMutation.error;

  return (
    <div className={cn(containerClass, className)} data-slot="claim-public-mailbox-card">
      {variant !== 'inline' ? (
        <div className="flex items-start gap-3">
          <span className="flex size-10 shrink-0 items-center justify-center rounded-md bg-primary/15 text-primary">
            <Mail className="size-5" aria-hidden="true" />
          </span>
          <div className="min-w-0 flex-1 space-y-1">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-base font-semibold text-foreground">
                {t('publicMailbox.title', { domain: activeDomain })}
              </h2>
              <Badge variant="secondary" className="gap-1 text-[11px]">
                <Sparkles className="size-3" aria-hidden="true" />
                {t('publicMailbox.badge')}
              </Badge>
            </div>
            <p className="text-sm text-muted-foreground">{t('publicMailbox.description')}</p>
          </div>
        </div>
      ) : null}

      <form
        onSubmit={event => void handleSubmit(event)}
        aria-busy={isSubmitting}
        className={cn('space-y-3', variant !== 'inline' && 'mt-4')}
      >
        {displayError ? (
          <Alert variant="destructive">
            <AlertCircle aria-hidden="true" className="size-4" />
            <AlertDescription>{t(resolveClaimErrorKey(displayError))}</AlertDescription>
          </Alert>
        ) : null}

        <div className="space-y-1.5">
          <Label htmlFor={inputId}>{t('publicMailbox.handleLabel')}</Label>
          <div className="flex items-stretch">
            <Input
              ref={inputRef}
              id={inputId}
              name="local_part"
              autoComplete="off"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              maxLength={MAILBOX_LOCAL_PART_MAX_LENGTH}
              value={localPart}
              disabled={isSubmitting}
              placeholder={t('publicMailbox.handlePlaceholder')}
              className="rounded-r-none border-r-0 bg-background focus-visible:z-10"
              onChange={event => {
                setTouched(true);
                setLocalPart(event.target.value);
                setSubmitError(null);
                claimMutation.reset();
              }}
            />
            {options.domains.length > 1 ? (
              <Select
                value={activeDomain}
                onValueChange={value => {
                  setSelectedDomain(value);
                  setSubmitError(null);
                }}
                disabled={isSubmitting}
              >
                <SelectTrigger
                  id={domainId}
                  aria-label={t('publicMailbox.domainLabel')}
                  className="w-auto min-w-[132px] shrink-0 rounded-l-none bg-muted/60 font-medium text-foreground"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent align="end">
                  {options.domains.map(domain => (
                    <SelectItem key={domain} value={domain}>
                      @{domain}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <span
                className="inline-flex shrink-0 items-center rounded-r-md border border-input bg-muted/60 px-3 text-sm font-medium text-muted-foreground select-none"
                aria-hidden="true"
              >
                @{activeDomain}
              </span>
            )}
          </div>

          <div
            aria-live="polite"
            className="min-h-5 pt-0.5 text-xs"
            data-slot="public-mailbox-availability"
          >
            {isChecking ? (
              <p className="flex items-center gap-1.5 text-muted-foreground">
                <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                <span>{t('publicMailbox.checking')}</span>
              </p>
            ) : status === 'available' && availabilityData ? (
              <p className="flex items-center gap-1.5 font-medium text-emerald-600 dark:text-emerald-400">
                <CheckCircle2 className="size-3.5 shrink-0" aria-hidden="true" />
                <span>{t('publicMailbox.available', { address: availabilityData.address })}</span>
              </p>
            ) : status === 'taken' && availabilityData ? (
              <p className="flex items-center gap-1.5 text-amber-700 dark:text-amber-400">
                <AlertCircle className="size-3.5 shrink-0" aria-hidden="true" />
                <span>{t('publicMailbox.taken', { address: availabilityData.address })}</span>
              </p>
            ) : status === 'reserved' ? (
              <p className="flex items-center gap-1.5 text-amber-700 dark:text-amber-400">
                <AlertCircle className="size-3.5 shrink-0" aria-hidden="true" />
                <span>{t('publicMailbox.reserved')}</span>
              </p>
            ) : status === 'too_short' ? (
              <p className="flex items-center gap-1.5 text-amber-700 dark:text-amber-400">
                <AlertCircle className="size-3.5 shrink-0" aria-hidden="true" />
                <span>{t('publicMailbox.tooShort', { min: options.min_local_length })}</span>
              </p>
            ) : status === 'invalid' ? (
              <p className="flex items-center gap-1.5 text-destructive">
                <AlertCircle className="size-3.5 shrink-0" aria-hidden="true" />
                <span>{t('publicMailbox.invalid')}</span>
              </p>
            ) : availability.isError && trimmedLocalPart.length > 0 ? (
              <p className="text-muted-foreground">{t('publicMailbox.checkFailed')}</p>
            ) : null}

            {isUnavailableStatus && availabilityData && availabilityData.suggestions.length > 0 ? (
              <div className="mt-2 flex flex-wrap items-center gap-1.5">
                <span className="text-muted-foreground">{t('publicMailbox.suggestionsLabel')}</span>
                {availabilityData.suggestions.map(suggestion => {
                  const local = extractSuggestedLocalPart(suggestion);
                  const display = suggestion.includes('@')
                    ? suggestion
                    : `${local}@${activeDomain}`;
                  return (
                    <button
                      key={suggestion}
                      type="button"
                      disabled={isSubmitting}
                      onClick={() => handleSuggestionPick(suggestion)}
                      className="inline-flex items-center rounded-full border border-border bg-background px-2.5 py-0.5 text-xs font-medium text-foreground transition-colors hover:border-primary/50 hover:bg-primary/5 focus-ring"
                    >
                      {display}
                    </button>
                  );
                })}
              </div>
            ) : null}
          </div>
        </div>

        <p className="text-xs leading-relaxed text-muted-foreground">
          {t('publicMailbox.usageLine', {
            newLimit: options.daily_send_limit_new,
            regularLimit: options.daily_send_limit,
          })}
        </p>

        <div className="flex flex-wrap items-center gap-2 pt-0.5">
          <Button type="submit" loading={isSubmitting} disabled={!canSubmit}>
            {organizationId === undefined
              ? t('publicMailbox.submit')
              : t('publicMailbox.submitInInbox')}
          </Button>
          {onSkip ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              loading={skipPending}
              disabled={isSubmitting}
              onClick={onSkip}
            >
              {t('publicMailbox.orConnectOnly')}
            </Button>
          ) : null}
        </div>
      </form>
    </div>
  );
}
