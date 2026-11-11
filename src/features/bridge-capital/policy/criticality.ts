/** Merchant category to spend criticality (spec §4). */
import type { SpendCriticality } from '../types.js';
import { CRITICALITY_BY_CATEGORY, DEFAULT_CRITICALITY } from './policy.config.js';

/** Categories are matched case-insensitively; unknown ones fall back to standard. */
export function criticalityForCategory(merchantCategory: string): SpendCriticality {
  return CRITICALITY_BY_CATEGORY[merchantCategory.trim().toLowerCase()] ?? DEFAULT_CRITICALITY;
}
