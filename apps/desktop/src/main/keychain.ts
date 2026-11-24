import fs from 'node:fs';
import path from 'node:path';

/**
 * Minimal adapter over Electron's `safeStorage` API so unit tests can inject
 * deterministic encryption doubles without launching an Electron runtime.
 */
export interface SafeStorageAdapter {
  isEncryptionAvailable(): boolean;
  encryptString(plainText: string): Buffer;
  decryptString(encrypted: Buffer): string;
}

export interface KeychainStorageBackend {
  read(): string | null;
  write(serialized: string): void;
}

interface EncryptedTokenRecord {
  ciphertext: string;
  updatedAt: string;
}

interface KeychainEnvelope {
  version: 1;
  encrypted: true;
  accounts: Record<string, EncryptedTokenRecord>;
}

export class KeychainUnavailableError extends Error {
  constructor(message = 'OS keychain encryption is unavailable; refusing to persist plaintext token.') {
    super(message);
    this.name = 'KeychainUnavailableError';
  }
}

export function createMemoryKeychainBackend(initialData: string | null = null): KeychainStorageBackend {
  let payload: string | null = initialData;
  return {
    read: () => payload,
    write: (serialized: string) => {
      payload = serialized;
    },
  };
}

/**
 * Persists encrypted token envelopes atomically with owner-only (0600) permissions.
 */
export function createFileKeychainBackend(filePath: string): KeychainStorageBackend {
  return {
    read(): string | null {
      if (!fs.existsSync(filePath)) {
        return null;
      }
      return fs.readFileSync(filePath, 'utf8');
    },
    write(serialized: string): void {
      const dir = path.dirname(filePath);
      fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
      const tmpPath = `${filePath}.${process.pid}.${Date.now()}.tmp`;
      fs.writeFileSync(tmpPath, serialized, { encoding: 'utf8', mode: 0o600 });
      fs.renameSync(tmpPath, filePath);
      fs.chmodSync(filePath, 0o600);
    },
  };
}

function emptyEnvelope(): KeychainEnvelope {
  return {
    version: 1,
    encrypted: true,
    accounts: {},
  };
}

function normalizeAccountId(accountId: string): string {
  const trimmed = accountId.trim();
  if (!trimmed) {
    throw new Error('accountId must be a non-empty string');
  }
  return trimmed;
}

/**
 * Secure Bearer Token store backed by Electron `safeStorage`.
 * Plaintext tokens are never serialized or written to disk.
 */
export class KeychainStore {
  private readonly safeStorage: SafeStorageAdapter;
  private readonly backend: KeychainStorageBackend;
  private envelope: KeychainEnvelope;

  constructor(safeStorage: SafeStorageAdapter, backend: KeychainStorageBackend) {
    this.safeStorage = safeStorage;
    this.backend = backend;
    this.envelope = this.loadEnvelope();
  }

  private assertEncryptionAvailable(): void {
    if (!this.safeStorage.isEncryptionAvailable()) {
      throw new KeychainUnavailableError();
    }
  }

  private loadEnvelope(): KeychainEnvelope {
    const raw = this.backend.read();
    if (!raw) {
      return emptyEnvelope();
    }
    try {
      const parsed = JSON.parse(raw) as Partial<KeychainEnvelope>;
      if (parsed.version !== 1 || parsed.encrypted !== true || typeof parsed.accounts !== 'object' || !parsed.accounts) {
        return emptyEnvelope();
      }
      const accounts: Record<string, EncryptedTokenRecord> = {};
      for (const [accountId, record] of Object.entries(parsed.accounts)) {
        if (record && typeof record.ciphertext === 'string' && typeof record.updatedAt === 'string') {
          accounts[accountId] = {
            ciphertext: record.ciphertext,
            updatedAt: record.updatedAt,
          };
        }
      }
      return { version: 1, encrypted: true, accounts };
    } catch {
      return emptyEnvelope();
    }
  }

  private persist(): void {
    this.backend.write(JSON.stringify(this.envelope, null, 2));
  }

  setToken(accountId: string, token: string): void {
    this.assertEncryptionAvailable();
    const id = normalizeAccountId(accountId);
    const trimmedToken = token.trim();
    if (!trimmedToken) {
      throw new Error('Bearer token must be a non-empty string');
    }

    const encrypted = this.safeStorage.encryptString(trimmedToken);
    if (!Buffer.isBuffer(encrypted) || encrypted.length === 0) {
      throw new KeychainUnavailableError('safeStorage returned an empty encrypted buffer.');
    }

    this.envelope.accounts[id] = {
      ciphertext: encrypted.toString('base64'),
      updatedAt: new Date().toISOString(),
    };
    this.persist();
  }

  getToken(accountId: string): string | null {
    const id = normalizeAccountId(accountId);
    const record = this.envelope.accounts[id];
    if (!record) {
      return null;
    }
    this.assertEncryptionAvailable();
    const buffer = Buffer.from(record.ciphertext, 'base64');
    return this.safeStorage.decryptString(buffer);
  }

  hasToken(accountId: string): boolean {
    const id = normalizeAccountId(accountId);
    return Boolean(this.envelope.accounts[id]);
  }

  deleteToken(accountId: string): boolean {
    const id = normalizeAccountId(accountId);
    if (!this.envelope.accounts[id]) {
      return false;
    }
    delete this.envelope.accounts[id];
    this.persist();
    return true;
  }

  listAccountIds(): string[] {
    return Object.keys(this.envelope.accounts).sort();
  }

  clearAll(): void {
    this.envelope = emptyEnvelope();
    this.persist();
  }
}
