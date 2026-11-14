export const MEDIA_SPECTRUM_BANDS = 8;

/** One reusable, logarithmic 80–8000 Hz spectrum for an existing audio graph. */
export function createMediaSpectrum(analyser: AnalyserNode, sampleRate: number) {
  analyser.fftSize = 1024;
  analyser.smoothingTimeConstant = 0.6;
  const bands = new Float32Array(MEDIA_SPECTRUM_BANDS);
  const binCount = analyser.frequencyBinCount || analyser.fftSize / 2;
  const floatData = typeof analyser.getFloatFrequencyData === "function" ? new Float32Array(binCount) : undefined;
  const byteData = !floatData && typeof analyser.getByteFrequencyData === "function" ? new Uint8Array(binCount) : undefined;
  const starts = new Uint16Array(MEDIA_SPECTRUM_BANDS);
  const ends = new Uint16Array(MEDIA_SPECTRUM_BANDS);
  const binHz = sampleRate / analyser.fftSize;
  const upperHz = Math.min(8000, sampleRate / 2);
  for (let band = 0; band < bands.length; band++) {
    starts[band] = Math.min(binCount - 1, Math.max(1, Math.floor(80 * (upperHz / 80) ** (band / bands.length) / binHz)));
    ends[band] = Math.min(binCount, Math.max(starts[band] + 1, Math.floor(80 * (upperHz / 80) ** ((band + 1) / bands.length) / binHz)));
  }
  let measuredAt = -Infinity;
  return {
    bands,
    sample(gain = 1) {
      if (!Number.isFinite(gain) || gain <= 0 || (!floatData && !byteData)) { bands.fill(0); measuredAt = -Infinity; return bands; }
      const now = performance.now();
      if (now - measuredAt < 1000 / 30) return bands;
      measuredAt = now;
      if (floatData) analyser.getFloatFrequencyData(floatData);
      else if (byteData) analyser.getByteFrequencyData(byteData);
      for (let band = 0; band < bands.length; band++) {
        let power = 0;
        for (let bin = starts[band]; bin < ends[band]; bin++) {
          const minDb = analyser.minDecibels ?? -100;
          const maxDb = analyser.maxDecibels ?? -30;
          const db = floatData ? floatData[bin] : minDb + ((byteData?.[bin] ?? 0) / 255) * (maxDb - minDb);
          if (Number.isFinite(db) && db > -80) power += 10 ** (db / 10);
        }
        // Fixed dB scale, never normalize each band against the current frame's loudest bin.
        const rmsDb = power > 0 ? 10 * Math.log10(power / (ends[band] - starts[band])) : -Infinity;
        bands[band] = Math.max(0, Math.min(1, (rmsDb + 72) / 54)) * Math.min(1, gain);
      }
      return bands;
    },
  };
}
