import { MutationObserver, onlineManager } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { createQueryClient } from '@/config/query-client';
import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';

describe('createQueryClient', () => {
  afterEach(() => onlineManager.setOnline(true));

  // Issue #107: an offline write fails at once instead of running unseen after reconnect.
  it('runs a mutation while offline instead of pausing it', async () => {
    onlineManager.setOnline(false);
    const mutationFn = vi.fn().mockRejectedValue(new Error('offline'));
    const observer = new MutationObserver(createQueryClient(), {
      mutationFn,
      meta: LOCAL_ERROR_HANDLING_META,
    });

    await expect(observer.mutate(undefined)).rejects.toThrow('offline');
    expect(mutationFn).toHaveBeenCalledTimes(1);
  });
});
