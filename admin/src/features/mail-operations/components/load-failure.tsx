import { RotateCw, ShieldOff, TriangleAlert } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { isCapabilityError } from '@/features/mail-operations/hooks/use-mail-operations';

interface LoadFailureProps {
  error: unknown;
  onRetry: () => void;
  retrying?: boolean;
}

/**
 * A failed load is never shown as zero: either the grant does not cover the view, or the request
 * failed and can be retried.
 */
export function LoadFailure({ error, onRetry, retrying = false }: LoadFailureProps) {
  const { t } = useTranslation();
  if (isCapabilityError(error)) {
    return (
      <div role="alert" className="flex items-start gap-3 p-4 text-sm">
        <ShieldOff aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div>
          <p className="font-medium">{t('mailOperations.forbidden.title')}</p>
          <p className="text-muted-foreground">{t('mailOperations.forbidden.description')}</p>
        </div>
      </div>
    );
  }
  return (
    <div role="alert" className="flex flex-wrap items-center gap-3 p-4 text-sm">
      <TriangleAlert aria-hidden="true" className="size-4 shrink-0 text-danger-strong" />
      <p className="flex-1 text-destructive">{t('mailOperations.errors.unavailable')}</p>
      <Button type="button" variant="outline" size="sm" onClick={onRetry} disabled={retrying}>
        <RotateCw aria-hidden="true" />
        {t('mailOperations.retry')}
      </Button>
    </div>
  );
}
