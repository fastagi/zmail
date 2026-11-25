const utf8Encoder = new TextEncoder();

export const CONTACT_IMPORT_CHUNK_MAX_ROWS = 300;
export const CONTACT_IMPORT_CHUNK_MAX_BYTES = 200 * 1_024;

export interface CsvChunk {
  csv: string;
  /** Number of data rows in this chunk (excluding header). */
  rowCount: number;
  /** 0-based index of the first data row in the original CSV. */
  startRowIndex: number;
}

export interface ParsedCsvRecords {
  header: string;
  rows: string[];
}

/**
 * Splits UTF-8 CSV text into raw record strings (header + non-empty data rows),
 * preserving RFC 4180 quoted fields that contain embedded newlines.
 */
export function parseCsvRecords(text: string): ParsedCsvRecords {
  const clean = text.startsWith('\uFEFF') ? text.slice(1) : text;
  const records: string[] = [];
  let inQuotes = false;
  let start = 0;

  for (let i = 0; i < clean.length; i++) {
    const ch = clean[i];
    if (ch === '"') {
      if (inQuotes && clean[i + 1] === '"') {
        i++;
      } else {
        inQuotes = !inQuotes;
      }
      continue;
    }
    if (!inQuotes && (ch === '\n' || ch === '\r')) {
      const record = clean.slice(start, i);
      if (record.trim() !== '') {
        records.push(record);
      }
      if (ch === '\r' && clean[i + 1] === '\n') {
        i++;
      }
      start = i + 1;
    }
  }

  if (start < clean.length) {
    const tail = clean.slice(start);
    if (tail.trim() !== '') {
      records.push(tail);
    }
  }

  if (records.length === 0) {
    return { header: '', rows: [] };
  }

  const [header, ...rows] = records;
  return { header: header ?? '', rows };
}

/**
 * Splits a CSV string into uploadable chunks bounded by row count and UTF-8 byte size.
 * Small CSVs that fit within a single chunk are returned verbatim as a single entry.
 */
export function buildCsvChunks(
  text: string,
  options?: { maxRows?: number; maxBytes?: number }
): CsvChunk[] {
  const maxRows = options?.maxRows ?? CONTACT_IMPORT_CHUNK_MAX_ROWS;
  const maxBytes = options?.maxBytes ?? CONTACT_IMPORT_CHUNK_MAX_BYTES;
  const { header, rows } = parseCsvRecords(text);

  if (rows.length === 0) {
    return [{ csv: text, rowCount: 0, startRowIndex: 0 }];
  }

  const totalBytes = utf8Encoder.encode(text).length;
  if (rows.length <= maxRows && totalBytes <= maxBytes) {
    return [{ csv: text, rowCount: rows.length, startRowIndex: 0 }];
  }

  const headerBytes = utf8Encoder.encode(`${header}\n`).length;
  const chunks: CsvChunk[] = [];
  let batch: string[] = [];
  let batchBytes = headerBytes;
  let batchStart = 0;

  for (let i = 0; i < rows.length; i++) {
    const row = rows[i] ?? '';
    const rowBytes = utf8Encoder.encode(`${row}\n`).length;
    const wouldExceedRows = batch.length >= maxRows;
    const wouldExceedBytes = batch.length > 0 && batchBytes + rowBytes > maxBytes;

    if (wouldExceedRows || wouldExceedBytes) {
      chunks.push({
        csv: `${header}\n${batch.join('\n')}\n`,
        rowCount: batch.length,
        startRowIndex: batchStart,
      });
      batch = [];
      batchBytes = headerBytes;
      batchStart = i;
    }

    batch.push(row);
    batchBytes += rowBytes;
  }

  if (batch.length > 0) {
    chunks.push({
      csv: `${header}\n${batch.join('\n')}\n`,
      rowCount: batch.length,
      startRowIndex: batchStart,
    });
  }

  return chunks;
}

export function formatFileSize(bytes: number): string {
  if (bytes < 1_024) return `${bytes} B`;
  if (bytes < 1_024 * 1_024) return `${(bytes / 1_024).toFixed(1)} KB`;
  return `${(bytes / (1_024 * 1_024)).toFixed(2)} MB`;
}
