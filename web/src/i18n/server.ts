import 'server-only';

import { getTranslations } from 'next-intl/server';
import {
  createTranslator,
  scopedTranslator,
  type AnyScopedTranslator,
  type AllScopePaths,
  type BaseTranslator,
  type ScopedTranslations,
  type UnifiedTranslations,
} from './translation-shared';

interface GetT {
  (): Promise<UnifiedTranslations>;
  <P extends AllScopePaths>(scope: P): Promise<ScopedTranslations<P>>;
}

// Cast through unknown for the reason given at createTranslator.
export const getT = (async (scope?: string): Promise<UnifiedTranslations | AnyScopedTranslator> => {
  const rootT = (await getTranslations()) as unknown as BaseTranslator;
  return scope ? scopedTranslator(rootT, scope) : createTranslator(rootT);
}) as unknown as GetT;
