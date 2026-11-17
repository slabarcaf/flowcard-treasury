import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import snapshot from '../../src/services/onboarding/industry-categories.json';
import metadata from '../../src/services/onboarding/industry-categories.source.json';
import { industryOptions } from '../../src/services/onboarding/industryReference.js';

describe('verified Airwallex industry export', () => {
  it('maps every exported code to a named selectable option without a live backend', () => {
    const options = industryOptions(snapshot);
    expect(snapshot).toHaveLength(metadata.category_count);
    expect(options).toHaveLength(metadata.unique_code_count);
    expect(new Set(options.map(option => option.value)).size).toBe(options.length);
    expect(options.every(option => option.label && /^ICCV3_[A-Z0-9]+$/.test(option.value))).toBe(true);
  });
  it('does not include credentials or authentication responses', () => {
    const text = readFileSync(new URL('../../src/services/onboarding/industry-categories.json', import.meta.url), 'utf8');
    expect(text).not.toMatch(/api[_-]?key|client[_-]?id|access[_-]?token|authorization|Bearer\s/i);
    expect(metadata.source).toBe('https://api.sandbox.airwallex.com/api/v1/reference/industry_categories');
    expect(Number.isNaN(Date.parse(metadata.fetched_at))).toBe(false);
  });
});
