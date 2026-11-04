const BYTES_PER_MB = BigInt(1_000_000);

export function normalizeMBValue(raw: string): string {
  const trimmed = raw.trim();
  if (!/^\d+$/.test(trimmed)) return trimmed;
  const bytes = BigInt(trimmed);
  const fraction = String(bytes % BYTES_PER_MB).padStart(6, "0").replace(/0+$/, "");
  return `${bytes / BYTES_PER_MB}${fraction ? `.${fraction}` : ""}`;
}

export function denormalizeMBValue(raw: string): string {
  const trimmed = raw.trim();
  if (!/^\d+(?:\.\d{1,6})?$/.test(trimmed)) return trimmed;
  const [whole, fraction = ""] = trimmed.split(".");
  return String(BigInt(whole) * BYTES_PER_MB + BigInt(fraction.padEnd(6, "0")));
}
