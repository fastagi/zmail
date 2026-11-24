'use client';

import { useRef, useState, type FormEvent } from 'react';
import { toast } from 'sonner';

import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useConnectMailDomain, usePreviewMailDomain } from '@/features/mailbox/hooks/use-mailbox';
import type { MailDomainCapability, MailDomainPreview } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';

/**
 * This form needs no token: the API connects the domain with the server's own Cloudflare account,
 * so a refusal there says so instead of pointing at a token the user never entered (#130).
 */
export function connectDomainErrorKey(failure: unknown) {
  if (
    failure instanceof ApiError &&
    failure.errorCode === ApiErrorCode.MAILBOX_DOMAIN_PROVIDER_FAILED
  ) {
    return 'domain.connectProviderFailed' as const;
  }
  return resolveMailboxErrorKey(failure);
}

/**
 * Headless state and submission workflow for connecting a new mail domain, including debounced
 * DNS preview lookup and capability confirmation.
 */
export function useConnectDomainForm() {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const connect = useConnectMailDomain(organizationId);
  const preview = usePreviewMailDomain(organizationId);
  const [name, setName] = useState('');
  const [capability, setCapability] = useState<MailDomainCapability>('send_receive');
  // True once the user picked a capability; a lookup then never overrides it.
  const [chosen, setChosen] = useState(false);
  // The last preview, shown only while the name it was asked for is still in the field.
  const [found, setFound] = useState<{ query: string; preview: MailDomainPreview } | null>(null);
  const [error, setError] = useState<string | null>(null);
  // A lookup changed the capability on submit; the user confirms by submitting again.
  const [confirming, setConfirming] = useState(false);
  // Held from the start of a submit until its connect settles; the ref stops re-entry at once.
  const submitting = useRef(false);
  const [busy, setBusy] = useState(false);
  // The lookup in flight, shared by leaving the field and submitting.
  const lookup = useRef<{ query: string; result: Promise<MailDomainPreview | null> } | null>(null);
  // What async lookups read when they land: the name in the field and whether a choice was made.
  const latest = useRef({ name: '', chosen: false });
  const value = name.trim();
  const current = found && found.query === value ? found.preview : null;

  /** Looks the name up once; the suggestion applies unless the user picked a capability. */
  const lookUp = (query: string): Promise<MailDomainPreview | null> => {
    if (found && found.query === query) return Promise.resolve(found.preview);
    if (lookup.current?.query === query) return lookup.current.result;
    const result = preview.mutateAsync(query).then(
      answer => {
        if (latest.current.name === query) {
          setFound({ query, preview: answer });
          if (!latest.current.chosen) setCapability(answer.suggested_capability);
        }
        return answer;
      },
      // The preview only suggests; connecting reports a bad name itself.
      () => null
    );
    lookup.current = { query, result };
    void result.finally(() => {
      if (lookup.current?.result === result) lookup.current = null;
    });
    return result;
  };

  const choose = (option: MailDomainCapability) => {
    setConfirming(false);
    latest.current.chosen = true;
    setChosen(true);
    setCapability(option);
  };

  const changeName = (next: string) => {
    const trimmed = next.trim();
    latest.current.name = trimmed;
    setName(next);
    setConfirming(false);
    if (chosen) return;
    // A suggestion belongs to the name it was made for.
    setCapability(
      found && found.query === trimmed ? found.preview.suggested_capability : 'send_receive'
    );
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    // One submit at a time: a second click while the lookup or the connect runs is ignored.
    if (submitting.current) return;
    setError(null);
    setConfirming(false);
    if (value === '') {
      setError(t('errors.domainInvalid'));
      return;
    }
    submitting.current = true;
    setBusy(true);
    try {
      // Look the name up before connecting it, so a mail service elsewhere is noticed. A failed
      // lookup suggests nothing, and the name then connects with the selected capability.
      const suggestion = await lookUp(value);
      if (suggestion && !latest.current.chosen && suggestion.suggested_capability !== capability) {
        // The suggestion now shows; submitting again confirms it.
        setConfirming(true);
        return;
      }
      const domain = await connect.mutateAsync({
        name: value,
        capability:
          suggestion && !latest.current.chosen ? suggestion.suggested_capability : capability,
      });
      setName('');
      setFound(null);
      setChosen(false);
      latest.current = { name: '', chosen: false };
      setCapability('send_receive');
      toast.success(
        t(
          domain.setting_up
            ? 'domain.settingUpToast'
            : domain.status === 'verified'
              ? 'domain.verified'
              : 'domain.connected'
        )
      );
    } catch (failure) {
      setError(t(connectDomainErrorKey(failure)));
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  };

  return {
    name,
    value,
    capability,
    current,
    confirming,
    error,
    busy,
    lookUp,
    choose,
    changeName,
    submit,
  };
}
