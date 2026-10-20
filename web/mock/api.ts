// A stand-in for the daemon's API, served by `npm run dev:mock`, so the UI can be built and
// looked at without a server. It holds believable data in memory and answers in the shapes of
// internal/api; nothing here ships in the build.
import type { Plugin } from 'vite';
import type { Addon, Backup, Domain, Project, Release, ServerStatus, Service } from '../src/api.ts';

const MB = 1024 ** 2;
const GB = 1024 ** 3;
const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();

type Fixture = Project & {
  services: Service[];
  addons: Addon[];
  releases: Release[];
  domains: Domain[];
  env_keys: string[];
  backups: Backup[];
};

const sslip = '203-0-113-7.sslip.io';

function fixtures(): Fixture[] {
  return [
    {
      name: 'leryo',
      created_at: ago(60 * 24 * 41),
      live_release: 12,
      live_since: ago(60 * 26),
      services: [
        {
          name: 'app',
          port: 3000,
          command: 'node src/server.ts',
          urls: ['https://app.leryo.eu', `https://app.leryo.${sslip}`],
          state: 'running',
          restarts: 0,
          started_at: ago(60 * 26),
          memory_limit: 512 * MB,
          stats: { cpu_percent: 3.2, memory_bytes: 182 * MB, memory_limit: 512 * MB },
        },
        {
          name: 'worker',
          command: 'node src/worker.ts',
          urls: [],
          state: 'running',
          restarts: 2,
          started_at: ago(60 * 3),
          memory_limit: 256 * MB,
          stats: { cpu_percent: 0.8, memory_bytes: 96 * MB, memory_limit: 256 * MB },
        },
      ],
      addons: [
        {
          kind: 'postgres',
          image: 'postgres:18.6-alpine',
          env_var: 'DATABASE_URL',
          url: 'postgres://postgres:…@postgres:5432/leryo',
          status: 'healthy',
          created_at: ago(60 * 24 * 41),
        },
      ],
      releases: [
        { seq: 13, status: 'failed', created_at: ago(38), commit: '9c41e07d2', commit_message: 'feat: invoices table', error: 'release step exited with 1\nerror: relation "invoice_lines" already exists\n    at migrate (src/db/migrate.ts:41:11)' },
        { seq: 12, status: 'live', created_at: ago(60 * 26), commit: '3a77de6f1', commit_message: 'fix: upload size limit to 300 MB' },
        { seq: 11, status: 'superseded', created_at: ago(60 * 24 * 3), commit: 'b0e2c94aa', commit_message: 'feat: SSE progress for imports', can_roll_back: true },
        { seq: 10, status: 'superseded', created_at: ago(60 * 24 * 4), commit: '51fd0a3c2-dirty', commit_message: 'chore: bump fastify', can_roll_back: true },
        { seq: 9, status: 'failed', created_at: ago(60 * 24 * 4 + 20), commit: '51fd0a3c2', commit_message: 'chore: bump fastify', error: 'health check failed: GET /api/ready returned 503 for 120 s' },
        { seq: 8, status: 'superseded', created_at: ago(60 * 24 * 9), commit: 'e8810bd47', commit_message: 'feat: pg-boss worker' },
        { seq: 7, status: 'superseded', created_at: ago(60 * 24 * 12), commit: '0f3a9e1c8', commit_message: 'fix: session cookie on sslip domain' },
        { seq: 6, status: 'superseded', created_at: ago(60 * 24 * 15), commit: 'c2d4410be', commit_message: 'feat: uploads volume' },
      ],
      domains: [{ hostname: 'app.leryo.eu', service: 'app' }],
      env_keys: ['BETTER_AUTH_SECRET', 'DATABASE_ADMIN_URL', 'DATABASE_URL', 'OPENROUTER_API_KEY', 'SMTP_URL', 'WEB_ORIGIN'],
      backups: [
        { name: '20261020T091402Z-before-r13.dump', size: 18_400_000, created_at: ago(38) },
        { name: '20261020T030000Z-daily.dump', size: 18_310_000, created_at: ago(60 * 7) },
        { name: '20261020T030000Z-daily.vol-uploads.tar', size: 412_000_000, created_at: ago(60 * 7) },
        { name: '20261019T030000Z-daily.dump', size: 18_220_000, created_at: ago(60 * 31) },
      ],
    },
    {
      name: 'ten00',
      created_at: ago(60 * 24 * 52),
      live_release: 9,
      live_since: ago(60 * 24 * 2),
      services: [
        {
          name: 'web',
          port: 3000,
          command: 'node server/main.ts',
          urls: [`https://web.ten00.${sslip}`],
          state: 'running',
          restarts: 0,
          started_at: ago(60 * 24 * 2),
          stats: { cpu_percent: 1.4, memory_bytes: 74 * MB, memory_limit: 0 },
        },
      ],
      addons: [
        {
          kind: 'redis',
          image: 'valkey/valkey:9.1.2-alpine',
          env_var: 'REDIS_URL',
          url: 'redis://:…@redis:6379',
          status: 'healthy',
          created_at: ago(60 * 24 * 52),
        },
      ],
      releases: [9, 8, 7, 6, 5, 4, 3, 2, 1].map((seq) => ({
        seq,
        status: (seq === 9 ? 'live' : 'superseded') as Release['status'],
        created_at: ago(60 * 24 * (2 + (9 - seq) * 3)),
        commit: `${seq}b7f0e2${seq}a`,
        commit_message: ['feat: daily puzzle', 'fix: ws reconnect', 'feat: leaderboard', 'chore: vite 8', 'fix: cache headers', 'feat: share card', 'feat: ws scores', 'feat: server/main.ts', 'init'][9 - seq],
        can_roll_back: seq >= 7,
      })),
      domains: [],
      env_keys: ['SESSION_SECRET'],
      backups: [{ name: '20261020T030000Z-daily.rdb', size: 210_000, created_at: ago(60 * 7) }],
    },
    {
      name: 'kino-bot',
      created_at: ago(15),
      services: [],
      addons: [],
      releases: [],
      domains: [],
      env_keys: ['BOT_TOKEN'],
      backups: [],
    },
  ];
}

const server: ServerStatus = {
  version: 'v1.0.0',
  disk_used: 31.4 * GB,
  disk_total: 75 * GB,
  memory_total: 7.7 * GB,
  memory_available: 5.1 * GB,
  docker_version: '29.1.2',
};

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const fail = (status: number, message: string, hint = '') => json(status, { error: { code: 'mock', message, hint } });
const noContent = () => new Response(null, { status: 204 });
const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** A body written line by line over time, as the daemon streams logs and operations. */
function stream(type: string, write: (send: (line: string) => void) => Promise<void>, keepOpen = false): Response {
  const enc = new TextEncoder();
  const body = new ReadableStream<Uint8Array>({
    async start(c) {
      await write((line) => c.enqueue(enc.encode(line)));
      if (!keepOpen) c.close();
    },
  });
  return new Response(body, { headers: { 'Content-Type': type } });
}

/**
 * The mock API as a fetch handler over its own copy of the sample data. `delay` is how long it
 * waits before each answer (streamed lines wait four times as long). A redeploy makes a new
 * release of the live image; a rollback makes the old release live again, as the daemon does.
 */
export function createMock({ delay = 150 }: { delay?: number } = {}) {
  const projects = fixtures();

  function operation(p: Fixture, image: Release | undefined, rollback: boolean) {
    const seq = rollback && image ? image.seq : Math.max(...p.releases.map((r) => r.seq)) + 1;
    const lines = [
      rollback ? `roll back to r${seq}` : `release r${seq} from r${image?.seq}'s image`,
      'start: services next to the live release',
      'health: GET /api/ready → 200 after 2.1 s',
      `switch: the proxy now routes to r${seq}`,
      'drain: stopped the previous containers',
    ];
    return stream('application/x-ndjson', async (send) => {
      for (const l of lines) {
        send(JSON.stringify({ log: l + '\n' }) + '\n');
        await wait(delay * 4);
      }
      for (const r of p.releases) if (r.status === 'live') Object.assign(r, { status: 'superseded', can_roll_back: true });
      if (rollback && image) image.status = 'live';
      else p.releases.unshift({ seq, status: 'live', created_at: new Date().toISOString(), commit: image?.commit, commit_message: image?.commit_message });
      p.live_release = seq;
      p.live_since = new Date().toISOString();
      send(JSON.stringify({ result: { ok: true, release: seq } }) + '\n');
    });
  }

  return async function handle(req: Request): Promise<Response> {
    const url = new URL(req.url);
    const path = url.pathname.replace(/^\/api\/v1\/?/, '').split('/').filter(Boolean).map(decodeURIComponent);
    const m = req.method;
    const body = async () => (req.body ? ((await req.json()) as Record<string, unknown>) : {});
    await wait(delay);
    if (path[0] === 'session') return noContent();
    if (path[0] === 'server') return json(200, server);
    if (path[0] !== 'projects') return fail(404, 'not found');
    if (path.length === 1) {
      if (m === 'POST') {
        const { name } = await body();
        const p: Fixture = { name: String(name), created_at: new Date().toISOString(), services: [], addons: [], releases: [], domains: [], env_keys: [], backups: [] };
        projects.push(p);
        return json(201, { name: p.name, created_at: p.created_at });
      }
      return json(200, projects.map(({ name, live_release, live_since, created_at }) => ({ name, live_release, live_since, created_at })));
    }
    const p = projects.find((x) => x.name === path[1]);
    if (!p) return fail(404, `no project ${path[1]}`, 'create it first');
    const [, , what, id] = path;
    switch (what) {
      case undefined: {
        const { backups: _, ...detail } = p;
        return json(200, { ...detail, services: p.services.map(({ stats: _s, ...s }) => s) });
      }
      case 'services':
        await wait(delay * 4);
        return json(200, p.services);
      case 'releases':
        return new Response(
          `$ docker buildx build --load -t kayly/${p.name}:r${id} .\n#1 [internal] load build definition\n#8 DONE 41.2s\nrelease: node src/db/migrate.ts\nhealth: GET /api/ready → 200\n`,
        );
      case 'logs': {
        const service = url.searchParams.get('service');
        return stream(
          'text/plain',
          async (send) => {
            for (let i = 0; i < 40; i++) {
              const at = new Date(Date.now() - (40 - i) * 9000).toISOString();
              send(`${at} ${service} {"level":30,"msg":"request completed","url":"/api/projects","status":200,"ms":${(i * 7) % 31}}\n`);
            }
          },
          true, // a followed log stays open
        );
      }
      case 'env':
        if (m === 'PUT' && id && !p.env_keys.includes(id)) p.env_keys.push(id);
        if (m === 'DELETE') p.env_keys = p.env_keys.filter((k) => k !== id);
        p.env_keys.sort();
        return noContent();
      case 'domains':
        if (m === 'POST') {
          const d = (await body()) as Domain;
          p.domains.push(d);
          return json(201, d);
        }
        p.domains = p.domains.filter((d) => d.hostname !== id);
        return noContent();
      case 'addons': {
        if (m === 'DELETE') {
          p.addons = p.addons.filter((a) => a.kind !== id);
          return noContent();
        }
        const { kind } = await body();
        await wait(delay * 10);
        p.addons.push({
          kind: kind as Addon['kind'],
          image: kind === 'postgres' ? 'postgres:18.6-alpine' : 'valkey/valkey:9.1.2-alpine',
          env_var: kind === 'postgres' ? 'DATABASE_URL' : 'REDIS_URL',
          url: '…',
          status: 'healthy',
          created_at: new Date().toISOString(),
        });
        return json(201, p.addons);
      }
      case 'backups':
        if (m === 'POST') {
          await wait(delay * 8);
          const name = `${new Date().toISOString().replace(/[-:]|\.\d+/g, '')}-manual.dump`;
          p.backups.unshift({ name, size: 18_420_000, created_at: new Date().toISOString() });
        }
        return json(200, p.backups);
      case 'redeploy':
        return operation(p, p.releases.find((r) => r.status === 'live'), false);
      case 'rollback': {
        const { release } = await body();
        return operation(p, p.releases.find((r) => r.seq === release), true);
      }
    }
    return fail(404, 'not found');
  };
}

/** Serves the mock under /api/v1 on Vite's dev server. */
export function mockApi(): Plugin {
  const handle = createMock();
  return {
    name: 'kayly-mock-api',
    configureServer(vite) {
      vite.middlewares.use('/api/v1', async (req, res) => {
        const chunks: Buffer[] = [];
        for await (const c of req) chunks.push(c as Buffer);
        const r = await handle(
          new Request(new URL('/api/v1' + (req.url ?? '/'), 'http://mock'), {
            method: req.method,
            body: chunks.length ? Buffer.concat(chunks) : undefined,
          }),
        );
        res.statusCode = r.status;
        r.headers.forEach((v, k) => res.setHeader(k, v));
        if (!r.body) return res.end();
        const reader = r.body.getReader();
        req.on('close', () => void reader.cancel());
        for (;;) {
          const { value, done } = await reader.read();
          if (done) return res.end();
          res.write(value);
        }
      });
    },
  };
}
