'use client';

import { useTranslations } from 'next-intl';
import {
  createTranslator,
  scopedTranslator,
  type AnyScopedTranslator,
  type AllScopePaths,
  type BaseTranslator,
  type ScopedTranslations,
  type UnifiedTranslations,
} from './translation-shared';

export type {
  AllScopePaths,
  AllTranslationKeys,
  ScopedTranslationKeys,
  ScopedTranslations,
  TranslationValue,
  TranslationVariables,
  Translators,
  UnifiedTranslations,
} from './translation-shared';

/**
 * Universal client-side translation hook.
 */
interface UseT {
  (): UnifiedTranslations;
  <P extends AllScopePaths>(scope: P): ScopedTranslations<P>;
}

// Cast through unknown for the reason given at createTranslator.
export const useT = ((scope?: string): UnifiedTranslations | AnyScopedTranslator => {
  const rootT = useTranslations() as unknown as BaseTranslator;
  return scope ? scopedTranslator(rootT, scope) : createTranslator(rootT);
}) as unknown as UseT;
