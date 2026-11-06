type DurationMS = number | null | undefined;

function validDurationMS(value: DurationMS): number | undefined {
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : undefined;
}

export function durationBetweenMS(startedAt: string | undefined, endedAt: string | undefined): number | undefined {
  if (!startedAt || !endedAt) {
    return undefined;
  }

  const startedMS = new Date(startedAt).getTime();
  const endedMS = new Date(endedAt).getTime();
  if (!Number.isFinite(startedMS) || !Number.isFinite(endedMS)) {
    return undefined;
  }
  return validDurationMS(endedMS - startedMS);
}

export function firstDurationMS(...values: DurationMS[]): number | undefined {
  for (const value of values) {
    const durationMS = validDurationMS(value);
    if (durationMS !== undefined) {
      return durationMS;
    }
  }
  return undefined;
}

export function sumDurationsMS(values: Iterable<DurationMS>): number | undefined {
  let totalMS = 0;
  for (const value of values) {
    totalMS += validDurationMS(value) ?? 0;
  }
  return validDurationMS(totalMS);
}

// Wall-clock duration of a complete observed timeline. Summing child durations
// double-counts parallel calls/replayed snapshots; missing timestamps do not
// provide enough evidence for a total, so individual timings remain separate.
export function timelineDurationMS(
  values: Iterable<{ startedAt?: string; endedAt?: string }>,
): number | undefined {
  let start = Number.POSITIVE_INFINITY;
  let end = Number.NEGATIVE_INFINITY;
  for (const value of values) {
    const itemStart = value.startedAt ? Date.parse(value.startedAt) : Number.NaN;
    const itemEnd = value.endedAt ? Date.parse(value.endedAt) : Number.NaN;
    if (!Number.isFinite(itemStart) || !Number.isFinite(itemEnd) || itemEnd < itemStart) return undefined;
    start = Math.min(start, itemStart);
    end = Math.max(end, itemEnd);
  }
  return validDurationMS(end - start);
}

export function formatDurationMS(value: DurationMS): string | undefined {
  const durationMS = validDurationMS(value);
  if (durationMS === undefined) {
    return undefined;
  }

  const seconds = durationMS / 1000;
  if (seconds < 10) {
    return `${Math.max(0.1, seconds).toFixed(1)}s`;
  }
  return `${Math.round(seconds)}s`;
}
