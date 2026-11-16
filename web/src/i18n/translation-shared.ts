import { AVAILABLE_MODULES, type ModuleName } from './module-names';
import type { MessageSchema } from './modules';
import type { ICUVariableNames } from './locale-message-shape';

export type TranslationValue = string | number | Date;
export type TranslationValues = Record<string, TranslationValue>;
export type BaseTranslator = (key: string, values?: TranslationValues) => string;

type StringKeyOf<T> = Extract<keyof T, string>;

type TranslationLeafPaths<T> = T extends object
  ? {
      [K in StringKeyOf<T>]: T[K] extends string
        ? K
        : T[K] extends object
          ? `${K}.${TranslationLeafPaths<T[K]> & string}`
          : never;
    }[StringKeyOf<T>]
  : never;

type TranslationScopePaths<T> = T extends object
  ? {
      [K in StringKeyOf<T>]: T[K] extends object
        ? K | `${K}.${TranslationScopePaths<T[K]> & string}`
        : never;
    }[StringKeyOf<T>]
  : never;

type ValueAtPath<T, Path extends string> = Path extends `${infer Head}.${infer Tail}`
  ? Head extends keyof T
    ? ValueAtPath<T[Head], Tail>
    : never
  : Path extends keyof T
    ? T[Path]
    : never;

export type AllTranslationKeys = TranslationLeafPaths<MessageSchema>;
export type AllScopePaths = TranslationScopePaths<MessageSchema>;
export type ScopedTranslationKeys<P extends AllScopePaths> = TranslationLeafPaths<
  ValueAtPath<MessageSchema, P>
>;

/** The ICU values a message needs, or never when it needs none. */
type MessageVariables<Message> = Message extends string
  ? [ICUVariableNames<Message>] extends [never]
    ? never
    : { [Name in ICUVariableNames<Message>]: TranslationValue }
  : never;

export type TranslationVariables<K extends AllTranslationKeys> = MessageVariables<
  ValueAtPath<MessageSchema, K>
>;

/**
 * Scoped keys resolve by walking the scope and then the key, never by joining them into one
 * template literal: the constraint of `${P}.${K}` crosses every scope with every key and grows
 * past what TypeScript can represent as the catalog grows.
 */
type ScopedVariables<P extends AllScopePaths, K extends string> = MessageVariables<
  ValueAtPath<ValueAtPath<MessageSchema, P>, K>
>;

type ExactVariables<Variables, Actual extends Variables> = Actual &
  Record<Exclude<keyof Actual, keyof Variables>, never>;

type ArgumentsFor<Variables, Actual extends Variables = Variables> = [Variables] extends [never]
  ? []
  : [values: ExactVariables<Variables, Actual>];

export type ScopedTranslations<P extends AllScopePaths> = <
  K extends ScopedTranslationKeys<P>,
  Actual extends ScopedVariables<P, K> = ScopedVariables<P, K>,
>(
  key: K,
  ...args: ArgumentsFor<ScopedVariables<P, K>, Actual>
) => string;

export type Translators = {
  [Namespace in ModuleName]: <
    K extends TranslationLeafPaths<MessageSchema[Namespace]>,
    Actual extends MessageVariables<ValueAtPath<MessageSchema[Namespace], K>> = MessageVariables<
      ValueAtPath<MessageSchema[Namespace], K>
    >,
  >(
    key: K,
    ...args: ArgumentsFor<MessageVariables<ValueAtPath<MessageSchema[Namespace], K>>, Actual>
  ) => string;
};

export type UnifiedTranslations = {
  <K extends AllTranslationKeys, Actual extends TranslationVariables<K> = TranslationVariables<K>>(
    key: K,
    ...args: ArgumentsFor<TranslationVariables<K>, Actual>
  ): string;
} & Translators;

type ScopedTranslatorMap = Translators;

function withScope(scope: string, key: string): string {
  return `${scope}.${key}`;
}

function createScopedTranslatorMap(rootT: BaseTranslator): ScopedTranslatorMap {
  return Object.fromEntries(
    AVAILABLE_MODULES.map(moduleName => [
      moduleName,
      (key: string, values?: TranslationValues) => rootT(withScope(moduleName, key), values),
    ])
  ) as ScopedTranslatorMap;
}

/**
 * The runtime shape of every scoped translator. Implementation signatures use it instead of
 * `ScopedTranslations<AllScopePaths>`, which crosses every scope with every key and grows past what
 * TypeScript can represent; callers still see the precise per-scope overloads.
 */
export type AnyScopedTranslator = (key: string, values?: TranslationValues) => string;

export function scopedTranslator(rootT: BaseTranslator, scope: string): AnyScopedTranslator {
  return (key: string, values?: TranslationValues) => rootT(withScope(scope, key), values);
}

interface CreateTranslator {
  (rootT: BaseTranslator): UnifiedTranslations;
  <P extends AllScopePaths>(rootT: BaseTranslator, scope: P): ScopedTranslations<P>;
}

// Cast through unknown: checking the implementation against the generic overload would cross
// every scope with every key, which grows past what TypeScript can represent.
export const createTranslator = ((
  rootT: BaseTranslator,
  scope?: string
): UnifiedTranslations | AnyScopedTranslator => {
  if (scope) {
    return scopedTranslator(rootT, scope);
  }

  return Object.assign(
    (key: AllTranslationKeys, values?: TranslationValues) => rootT(key, values),
    createScopedTranslatorMap(rootT)
  ) as UnifiedTranslations;
}) as unknown as CreateTranslator;
