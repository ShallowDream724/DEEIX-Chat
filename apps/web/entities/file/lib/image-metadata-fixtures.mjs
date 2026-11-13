export const bytes = (...parts) => new Uint8Array(Buffer.concat(parts.map(part => Buffer.from(part))));
export const u32 = value => [value >>> 24, (value >>> 16) & 255, (value >>> 8) & 255, value & 255];
export const chunk = (type, data) => bytes(u32(data.length), Buffer.from(type), data, [0, 0, 0, 0]);

// Header/container fixtures only. Browser decode tests use real image files.
export function png(width = 800, height = 600, extra = [], depth = 8, colorType = 6) {
  return bytes([137, 80, 78, 71, 13, 10, 26, 10], chunk("IHDR", bytes(u32(width), u32(height), [depth, colorType, 0, 0, 0])), ...extra, chunk("IDAT", [1]), chunk("IEND", []));
}

export function jpeg(width = 4000, height = 3000, segments = []) {
  return bytes([255, 216], ...segments, [255, 192, 0, 17, 8, height >>> 8, height & 255, width >>> 8, width & 255, 3, 1, 17, 0, 2, 17, 0, 3, 17, 0, 255, 218, 0, 8, 1, 1, 0, 0, 63, 0, 1, 255, 217]);
}
