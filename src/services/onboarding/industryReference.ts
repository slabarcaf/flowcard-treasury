import type { FormOption } from './options.js';

/** Preserve category/question context; never treat an arbitrary user string as a code. */
export function industryOptions(value: unknown): FormOption[] {
  if (!Array.isArray(value)) throw new Error('Industry list unavailable');
  const options: FormOption[] = [];
  const seen = new Set<string>();
  function visit(item: unknown, path: string[], depth: number) {
    if (depth > 12 || options.length > 10_000 || !item || typeof item !== 'object') return;
    const node = item as Record<string, unknown>;
    const type = String(node.type ?? '').toUpperCase();
    const text = typeof node.name === 'string' && node.name.trim() ? node.name.trim()
      : typeof node.question === 'string' ? node.question.trim() : '';
    const question = typeof node.question === 'string' && node.question.trim() !== text ? node.question.trim() : '';
    const label = [...path, ...(text ? [text] : []), ...(question ? [question] : []), ...(typeof node.answer === 'string' ? [node.answer] : [])];
    if (type === 'CODE' && typeof node.value === 'string' && /^ICCV3_[A-Z0-9]+$/.test(node.value) && label.length && !seen.has(node.value)) {
      options.push({ value: node.value, label: label.join(' › ') });
      seen.add(node.value);
    }
    if (Array.isArray(node.values)) for (const child of node.values) visit(child, label, depth + 1);
  }
  for (const item of value) visit(item, [], 0);
  if (!options.length) throw new Error('Industry list unavailable');
  return options.sort((a, b) => a.label.localeCompare(b.label, 'en'));
}
