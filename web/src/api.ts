// Client for the daemon's API (internal/api). The browser authenticates with the session
// cookie; every request carries the header that cookie-authenticated writes require.

export type Project = {
  name: string;
  live_release?: number;
  live_since?: string;
  created_at: string;
};

export type ReleaseStatus = 'building' | 'releasing' | 'starting' | 'live' | 'failed' | 'superseded';

export type Release = {
  seq: number;
  status: ReleaseStatus;
  error?: string;
  created_at: string;
  /** A previous release whose image is still kept. */
  can_roll_back?: boolean;
};

export type Service = { name: string; port?: number; command?: string; urls: string[] };

export type AddonKind = 'postgres' | 'redis';

export type Addon = {
  kind: AddonKind;
  image: string;
  env_var: string;
  url: string;
  status: string;
  created_at: string;
};

export type Domain = { hostname: string; service: string };

export type ProjectDetail = Project & {
  services: Service[];
  addons: Addon[];
  releases: Release[];
  domains: Domain[];
  env_keys: string[];
};

export type Backup = { name: string; size: number; created_at: string };

export type StreamResult = { ok: boolean; release?: number; error?: string; unknown?: boolean };

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly hint?: string,
  ) {
    super(message);
  }
}

const headers = { 'X-Kayly-Request': '1' };

async function request(method: string, path: string, body?: unknown): Promise<Response> {
  const res = await fetch('/api/v1' + path, {
    method,
    credentials: 'same-origin',
    headers: body === undefined ? headers : { ...headers, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`;
    let hint: string | undefined;
    try {
      const e = (await res.json()) as { error?: { message?: string; hint?: string } };
      message = e.error?.message || message;
      hint = e.error?.hint;
    } catch {
      // not JSON: keep the status line
    }
    throw new ApiError(res.status, message, hint);
  }
  return res;
}

async function json<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await request(method, path, body);
  return res.status === 204 ? (undefined as T) : ((await res.json()) as T);
}

const p = (name: string) => '/projects/' + encodeURIComponent(name);

export const api = {
  signIn: (token: string) => json<void>('POST', '/session', { token }),
  signOut: () => json<void>('DELETE', '/session'),

  projects: () => json<Project[]>('GET', '/projects'),
  createProject: (name: string) => json<Project>('POST', '/projects', { name }),
  project: (name: string) => json<ProjectDetail>('GET', p(name)),

  setEnv: (name: string, key: string, value: string) =>
    json<void>('PUT', `${p(name)}/env/${encodeURIComponent(key)}`, { value }),
  deleteEnv: (name: string, key: string) => json<void>('DELETE', `${p(name)}/env/${encodeURIComponent(key)}`),

  addDomain: (name: string, hostname: string, service: string) =>
    json<Domain>('POST', `${p(name)}/domains`, { hostname, service }),
  removeDomain: (name: string, hostname: string) =>
    json<void>('DELETE', `${p(name)}/domains/${encodeURIComponent(hostname)}`),

  addAddon: (name: string, kind: AddonKind) => json<Addon[]>('POST', `${p(name)}/addons`, { kind }),
  removeAddon: (name: string, kind: AddonKind) => json<void>('DELETE', `${p(name)}/addons/${kind}`),

  backups: (name: string) => json<Backup[]>('GET', `${p(name)}/backups`),
  createBackup: (name: string) => json<Backup>('POST', `${p(name)}/backups`),
  backupURL: (name: string, backup: string) => `/api/v1${p(name)}/backups/${encodeURIComponent(backup)}`,

  redeploy: (name: string, onLog: (text: string) => void) =>
    operation(`${p(name)}/redeploy`, undefined, onLog),
  rollback: (name: string, release: number, onLog: (text: string) => void) =>
    operation(`${p(name)}/rollback`, { release }, onLog),

  /** Streams a service's output to onText until signal aborts (follow) or the tail ends. */
  async logs(name: string, service: string, signal: AbortSignal, onText: (text: string) => void) {
    const q = new URLSearchParams({ service, tail: '200', follow: '1' });
    const res = await fetch(`/api/v1${p(name)}/logs?${q}`, { credentials: 'same-origin', headers, signal });
    if (!res.ok || !res.body) {
      let message = `${res.status} ${res.statusText}`;
      try {
        message = ((await res.json()) as { error?: { message?: string } }).error?.message || message;
      } catch {
        // keep the status line
      }
      throw new ApiError(res.status, message);
    }
    const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return;
      onText(value);
    }
  },
};

/** Runs a deploy-like operation, passing its log to onLog, and returns its result. */
async function operation(path: string, body: unknown, onLog: (text: string) => void): Promise<StreamResult> {
  const res = await request('POST', path, body);
  if (!res.body) throw new ApiError(0, 'The server sent no progress.');
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    const lines = (buf += value).split('\n');
    buf = lines.pop() ?? '';
    for (const line of lines) {
      if (!line) continue;
      const e = JSON.parse(line) as { log?: string; result?: StreamResult };
      if (e.result) return e.result;
      if (e.log) onLog(e.log);
    }
  }
  throw new ApiError(0, 'The connection ended before the result. Check the releases list.');
}
