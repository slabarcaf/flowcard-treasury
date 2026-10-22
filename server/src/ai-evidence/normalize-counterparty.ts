/** Normalize only case, punctuation, and whitespace; preserve all name tokens. */
export function normalizeCounterpartyName(name: string): string {
  return name
    .normalize("NFKC")
    .toLocaleLowerCase("en-US")
    .replace(/[\p{P}\p{S}]/gu, " ")
    .replace(/\s+/g, " ")
    .trim();
}

/** True only when both names explicitly normalize to the same non-empty value. */
export function sameExplicitCounterparty(left: string, right: string): boolean {
  const normalizedLeft = normalizeCounterpartyName(left);
  return normalizedLeft !== "" && normalizedLeft === normalizeCounterpartyName(right);
}
