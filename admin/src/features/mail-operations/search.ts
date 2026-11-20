import { z } from 'zod';
import { isSelectableDay } from '@/features/mail-operations/types';

/** The `date` search value of every Operations route: a past or current UTC day, else today. */
export const daySearchField = z
  .string()
  .refine((value) => isSelectableDay(value))
  .optional()
  .catch(undefined);
