import { Download, ExternalLink, Trash2 } from 'lucide-react';
import { Fragment, type SubmitEvent, useCallback, useEffect, useRef, useState } from 'react';
import type { Navigate } from './App';
import {
  ApiError,
  api,
  type Addon,
  type AddonKind,
  type Backup,
  type ProjectDetail,
  type Release,
  type ReleaseStatus,
  type StreamResult,
} from './api';
import { Logs } from './logs';
import {
  Button,
  Command,
  CopyButton,
  ErrorNote,
  Field,
  IconButton,
  Lamp,
  type LampState,
  Select,
  Sheet,
  SpikeStrip,
  StandbyGo,
  TextInput,
  When,
  formatBytes,
} from './ui';

type Operation = {
  title: string;
  log: string;
  result?: StreamResult;
};

/** Hook: the project's detail, refreshed every few seconds while the tab is visible. */
function useProject(name: string) {
  const [detail, setDetail] = useState<ProjectDetail | null>(null);
  const [error, setError] = useState<unknown>(null);
  const reload = useCallback(async () => {
    try {
      setDetail(await api.project(name));
      setError(null);
    } catch (e) {
      setError(e);
    }
  }, [name]);
  useEffect(() => {
    void reload();
    const t = setInterval(() => document.visibilityState === 'visible' && void reload(), 5000);
    return () => clearInterval(t);
  }, [reload]);
  return { detail, error, reload };
}

export function ProjectPage({
  name,
  navigate,
  onChanged,
}: {
  name: string;
  navigate: Navigate;
  onChanged: () => Promise<void>;
}) {
  const { detail, error, reload } = useProject(name);
  const [op, setOp] = useState<Operation | null>(null);
  const [arrived, setArrived] = useState<number | null>(null);
  // Env values and add-ons reach running services only through a new release.
  const [pending, setPending] = useState(false);

  const run = useCallback(
    async (title: string, start: (onLog: (t: string) => void) => Promise<StreamResult>, rollbackTo?: number) => {
      setOp({ title, log: '' });
      const before = Math.max(0, ...(detail?.releases.map((r) => r.seq) ?? []));
      let result: StreamResult;
      try {
        result = await start((t) => setOp((o) => (o ? { ...o, log: o.log + t } : o)));
      } catch (e) {
        if (e instanceof ApiError && e.status > 0) {
          result = { ok: false, error: e.message };
        } else {
          // The connection dropped, not the operation: the server finishes it regardless.
          setOp((o) => (o ? { ...o, log: o.log + '\nLost the connection. Checking the result…\n' } : o));
          result = await outcome(name, before, rollbackTo);
          const checked = result.ok
            ? `Checked: r${result.release} is live.`
            : result.unknown
              ? 'Checked: no result yet.'
              : `Checked: r${result.release} failed.`;
          setOp((o) => (o ? { ...o, log: o.log + checked + '\n' } : o));
        }
      }
      setOp((o) => (o ? { ...o, result } : o));
      if (result.ok) {
        setArrived(result.release ?? null);
        setPending(false);
      }
      await reload();
      await onChanged();
    },
    [reload, onChanged, detail, name],
  );

  if (error && !detail) {
    return (
      <div className="flex flex-col gap-3">
        <ErrorNote error={error} />
        <a
          href="/"
          onClick={(e) => {
            e.preventDefault();
            navigate('/');
          }}
          className="text-action underline"
        >
          All projects
        </a>
      </div>
    );
  }
  if (!detail) return <PageSkeleton />;

  const live = detail.releases.find((r) => r.status === 'live');
  const changed = () => {
    setPending(Boolean(live));
    void reload();
  };
  const redeploy = () => void run('Redeploy', (onLog) => api.redeploy(name, onLog));

  return (
    <div className="flex flex-col gap-5">
      <header className="flex flex-wrap items-start justify-between gap-x-6 gap-y-4">
        <div className="flex min-w-0 flex-col gap-2">
          <h1 className="flex items-center gap-3 text-title font-bold">
            <SpikeStrip name={name} />
            {name}
          </h1>
          {live ? (
            <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
              <p className="flex items-center gap-2.5">
                <Lamp state="live" size={18} arrive={arrived === live.seq} />
                <span className="font-mono text-cue font-bold tracking-tight">r{live.seq}</span>
                <span className="text-meta text-ink-2">
                  live since <When at={live.created_at} />
                </span>
              </p>
              <ServiceLinks detail={detail} />
            </div>
          ) : (
            <p className="flex items-center gap-2 text-ink-2">
              <Lamp state="off" />
              Not deployed yet
            </p>
          )}
        </div>
        {live && (
          <div className="flex flex-col items-end gap-1.5 max-sm:w-full max-sm:items-stretch">
            <StandbyGo
              standby="Redeploy"
              go={`Go: release r${live.seq}'s image again`}
              variant={pending ? 'primary' : 'quiet'}
              busy={Boolean(op && !op.result)}
              onGo={redeploy}
            />
            {pending && <span className="text-meta text-ink-2">New values and add-ons apply on redeploy</span>}
          </div>
        )}
      </header>

      <div className="grid items-start gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(320px,400px)]">
        <div className="flex min-w-0 flex-col gap-5">
          <Releases
            name={name}
            releases={detail.releases}
            op={op}
            arrived={arrived}
            onDismiss={() => setOp(null)}
            onRollback={(seq) => void run(`Roll back to r${seq}`, (onLog) => api.rollback(name, seq, onLog), seq)}
          />
          {live && <Logs name={name} services={detail.services.map((s) => s.name)} releaseSeq={live.seq} />}
        </div>
        <div className="flex min-w-0 flex-col gap-5">
          <Addons name={name} addons={detail.addons} onChanged={changed} />
          <Env name={name} keys={detail.env_keys} onChanged={changed} />
          <Domains detail={detail} onChanged={() => void reload()} />
          {detail.addons.some((a) => a.kind === 'postgres') && <Backups name={name} />}
        </div>
      </div>
    </div>
  );
}

/**
 * Reads the outcome of an operation whose progress stream broke: the newest release after
 * `before` (or the rollback target) settling as live or failed. Gives up after three minutes.
 */
async function outcome(name: string, before: number, rollbackTo?: number): Promise<StreamResult> {
  for (let i = 0; i < 120; i++) {
    await new Promise((r) => setTimeout(r, 1500));
    let d: ProjectDetail;
    try {
      d = await api.project(name);
    } catch {
      continue; // still offline
    }
    if (rollbackTo !== undefined) {
      const target = d.releases.find((r) => r.seq === rollbackTo);
      if (target?.status === 'live') return { ok: true, release: rollbackTo };
      if (i > 40) break;
      continue;
    }
    const next = d.releases.find((r) => r.seq > before);
    if (next?.status === 'live' || next?.status === 'superseded') return { ok: true, release: next.seq };
    if (next?.status === 'failed') return { ok: false, release: next.seq, error: next.error };
  }
  return { ok: false, unknown: true, error: 'Kayly did not answer in time. Check the releases below.' };
}

function ServiceLinks({ detail }: { detail: ProjectDetail }) {
  const links = detail.services.filter((s) => s.urls.length > 0);
  if (links.length === 0) return null;
  return (
    <ul className="flex min-w-0 flex-wrap gap-x-4 gap-y-1">
      {links.map((s) => (
        <li key={s.name} className="flex min-w-0 items-center gap-1.5 text-meta">
          {links.length > 1 && <span className="text-ink-2">{s.name}</span>}
          <a
            href={s.urls[0]}
            target="_blank"
            rel="noreferrer"
            className="inline-flex min-w-0 items-center gap-1 font-mono text-ink underline decoration-rule hover:decoration-ink"
          >
            <span className="truncate">{s.urls[0].replace(/^https:\/\//, '')}</span>
            <ExternalLink size={13} aria-hidden="true" className="shrink-0 text-ink-2" />
          </a>
        </li>
      ))}
    </ul>
  );
}

const statusWord: Record<ReleaseStatus, string> = {
  live: 'Live',
  superseded: 'Previous',
  failed: 'Failed',
  building: 'Building',
  releasing: 'Release step',
  starting: 'Starting',
};

function lampFor(s: ReleaseStatus): LampState {
  if (s === 'live' || s === 'failed' || s === 'superseded') return s;
  return 'standby';
}

/** The cue sheet: every release with its lamp, the live one reversed out on a plate. */
function Releases({
  name,
  releases,
  op,
  arrived,
  onDismiss,
  onRollback,
}: {
  name: string;
  releases: Release[];
  op: Operation | null;
  arrived: number | null;
  onDismiss: () => void;
  onRollback: (seq: number) => void;
}) {
  const busy = Boolean(op && !op.result);
  return (
    <Sheet id="releases" title="Releases">
      {op && <OperationLog op={op} onDismiss={onDismiss} />}
      {releases.length === 0 ? (
        <div className="flex flex-col gap-3 p-4">
          <p className="max-w-[62ch] text-ink-2">
            Deploy from the app's folder, next to its <code className="font-mono text-ink">kayly.toml</code>. Values
            and add-ons set here are ready for it.
          </p>
          <Command>{`kayly deploy -p ${name}`}</Command>
        </div>
      ) : (
        <table className="w-full border-collapse text-left">
          <thead>
            <tr className="border-b border-rule text-meta text-ink-2">
              <th scope="col" className="w-0 py-2 pr-2.5 pl-4 font-normal">
                <span className="sr-only">State</span>
              </th>
              <th scope="col" className="py-2 pr-3 font-semibold">
                Release
              </th>
              <th scope="col" className="py-2 pr-3 font-semibold max-sm:hidden">
                When
              </th>
              <th scope="col" className="w-full py-2 pr-3 font-semibold">
                Status
              </th>
              <th scope="col" className="py-2 pr-4">
                <span className="sr-only">Action</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {releases.map((r) => {
              const isLive = r.status === 'live';
              return (
                <tr
                  key={r.seq}
                  className={
                    isLive
                      ? 'bg-plate text-plate-ink'
                      : 'border-b border-rule last:border-b-0 hover:bg-hover'
                  }
                >
                  <td className="py-2.5 pr-2.5 pl-4 align-top">
                    <span className="flex h-5 items-center">
                      <Lamp state={lampFor(r.status)} arrive={isLive && arrived === r.seq} />
                    </span>
                  </td>
                  <td className="py-2.5 pr-3 align-top">
                    <span className="font-mono font-semibold">r{r.seq}</span>
                    <span className={`block text-meta whitespace-nowrap sm:hidden ${isLive ? 'text-plate-ink-2' : 'text-ink-2'}`}>
                      <When at={r.created_at} />
                    </span>
                  </td>
                  <td
                    className={`py-2.5 pr-3 align-top text-meta whitespace-nowrap max-sm:hidden ${isLive ? 'text-plate-ink-2' : 'text-ink-2'}`}
                  >
                    <span className="flex h-5 items-center">
                      <When at={r.created_at} />
                    </span>
                  </td>
                  <td className="min-w-0 py-2.5 pr-3 align-top">
                    <span
                      className={
                        r.status === 'failed'
                          ? 'font-semibold text-fail-ink'
                          : r.status === 'superseded'
                            ? 'text-ink-2'
                            : 'font-semibold'
                      }
                    >
                      {statusWord[r.status]}
                    </span>
                    {r.error && <FailureReason error={r.error} />}
                  </td>
                  <td className="py-1.5 pr-4 text-right align-top">
                    {r.status === 'superseded' &&
                      (r.can_roll_back ? (
                        <StandbyGo
                          small
                          standby="Roll back"
                          go={`Go: r${r.seq} live`}
                          busy={false}
                          onGo={() => !busy && onRollback(r.seq)}
                        />
                      ) : (
                        <span className="flex h-8 items-center justify-end text-meta whitespace-nowrap text-ink-2">
                          image pruned
                        </span>
                      ))}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </Sheet>
  );
}

function FailureReason({ error }: { error: string }) {
  const [first, ...rest] = error.split('\n');
  return (
    <span className="block text-meta text-ink-2">
      <Inline text={first} />
      {rest.length > 0 && (
        <details className="mt-1">
          <summary className="cursor-pointer text-ink">Full message</summary>
          <pre className="mt-1 max-h-48 overflow-auto font-mono whitespace-pre-wrap">{rest.join('\n')}</pre>
        </details>
      )}
    </span>
  );
}

/** Renders `quoted` spans of Kayly's messages as commands that never wrap mid-token. */
function Inline({ text }: { text: string }) {
  return (
    <>
      {text.split('`').map((part, i) =>
        i % 2 === 1 ? (
          <code key={i} className="font-mono text-ink">
            {/* Lines break between words only, never after a hyphen inside a flag. */}
            {part.split(' ').map((word, j) => (
              <Fragment key={j}>
                {j > 0 && ' '}
                <span className="whitespace-nowrap">{word}</span>
              </Fragment>
            ))}
          </code>
        ) : (
          part
        ),
      )}
    </>
  );
}

/** Live output of a redeploy or rollback while it runs, and its outcome after. */
function OperationLog({ op, onDismiss }: { op: Operation; onDismiss: () => void }) {
  const ref = useRef<HTMLPreElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [op.log]);
  const state: LampState = !op.result ? 'standby' : op.result.ok ? 'live' : 'failed';
  return (
    <div className="border-b border-rule">
      <div className="flex min-h-11 items-center gap-3 px-4 py-2">
        <Lamp state={state} />
        <p className="min-w-0 flex-1 font-semibold" aria-live="polite">
          {!op.result
            ? `${op.title}: standing by`
            : op.result.ok
              ? `${op.title}: r${op.result.release} is live`
              : op.result.unknown
                ? `${op.title}: result unknown`
                : `${op.title} failed. The live release kept serving.`}
        </p>
        {op.result && (
          <Button variant="quiet" onClick={onDismiss} className="h-8 px-2.5 text-meta">
            Close
          </Button>
        )}
      </div>
      {op.result && !op.result.ok && op.result.error && (
        <p className="px-4 pb-2 text-meta font-semibold text-fail-ink">{op.result.error}</p>
      )}
      <pre
        ref={ref}
        tabIndex={0}
        aria-label={`${op.title} output`}
        className="max-h-72 overflow-auto bg-code px-4 py-3 font-mono text-meta whitespace-pre-wrap text-ink"
      >
        {op.log.replace(/^\n+/, '') || 'Waiting for the first line…'}
      </pre>
    </div>
  );
}

const addonInfo: Record<AddonKind, { label: string; gives: string }> = {
  postgres: { label: 'Postgres', gives: 'DATABASE_URL' },
  redis: { label: 'Redis', gives: 'REDIS_URL' },
};

function addonLamp(a: Addon): LampState {
  if (a.status === 'healthy') return 'live';
  if (a.status === 'starting') return 'standby';
  return 'failed';
}

function Addons({ name, addons, onChanged }: { name: string; addons: Addon[]; onChanged: () => void }) {
  return (
    <Sheet quiet id="addons" title="Add-ons">
      <ul>
        {(['postgres', 'redis'] as const).map((kind) => {
          const a = addons.find((x) => x.kind === kind);
          return (
            <li key={kind} className="border-b border-rule last:border-b-0">
              {a ? (
                <AttachedAddon name={name} addon={a} onChanged={onChanged} />
              ) : (
                <AvailableAddon name={name} kind={kind} onChanged={onChanged} />
              )}
            </li>
          );
        })}
      </ul>
    </Sheet>
  );
}

function AvailableAddon({ name, kind, onChanged }: { name: string; kind: AddonKind; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const info = addonInfo[kind];
  return (
    <div className="flex flex-col gap-2 px-4 py-3">
      <div className="flex items-center gap-3">
        <Lamp state="off" />
        <div className="min-w-0 flex-1">
          <p className="font-semibold">{info.label}</p>
          <p className="text-meta text-ink-2">
            Services get <code className="font-mono">{info.gives}</code>
          </p>
        </div>
        <Button
          busy={busy}
          onClick={async () => {
            setBusy(true);
            setError(null);
            try {
              await api.addAddon(name, kind);
              onChanged();
            } catch (e) {
              setError(e);
            } finally {
              setBusy(false);
            }
          }}
        >
          {busy ? 'Starting' : `Add ${info.label}`}
        </Button>
      </div>
      <ErrorNote error={error} />
    </div>
  );
}

function AttachedAddon({ name, addon, onChanged }: { name: string; addon: Addon; onChanged: () => void }) {
  const [removing, setRemoving] = useState(false);
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const info = addonInfo[addon.kind];

  async function remove(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.removeAddon(name, addon.kind);
      onChanged();
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-3 px-4 py-3">
      <div className="flex items-center gap-3">
        <Lamp state={addonLamp(addon)} />
        <div className="min-w-0 flex-1">
          <p className="font-semibold">
            {info.label} <span className="font-normal text-ink-2">{addon.status}</span>
          </p>
          <p className="text-meta text-ink-2">
            <code className="font-mono">{addon.env_var}</code> since <When at={addon.created_at} />
          </p>
        </div>
        <CopyButton text={addon.url} label={`Copy ${addon.env_var}`} />
        <IconButton label={`Remove ${info.label}`} onClick={() => setRemoving((v) => !v)} aria-expanded={removing}>
          <Trash2 size={16} />
        </IconButton>
      </div>
      {removing && (
        <form onSubmit={remove} className="flex flex-col gap-2 rounded-md border border-fail/40 p-3">
          <p className="text-meta">
            This deletes {info.label} and all its data. Backups stay. Type <strong className="font-mono">{name}</strong> to
            confirm.
          </p>
          <div className="flex gap-2">
            <label className="min-w-0 flex-1">
              <span className="sr-only">Project name</span>
              <TextInput value={confirm} onChange={(e) => setConfirm(e.target.value)} className="font-mono" autoFocus />
            </label>
            <Button type="submit" variant="danger" busy={busy} disabled={confirm !== name}>
              Delete {info.label}
            </Button>
          </div>
          <ErrorNote error={error} />
        </form>
      )}
    </div>
  );
}

function Env({ name, keys, onChanged }: { name: string; keys: string[]; onChanged: () => void }) {
  const [key, setKey] = useState('');
  const [value, setValue] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.setEnv(name, key, value);
      setKey('');
      setValue('');
      onChanged();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Sheet quiet id="env" title="Env values">
      {keys.length > 0 && (
        <ul>
          {keys.map((k) => (
            <li key={k} className="flex min-h-11 items-center gap-2 border-b border-rule px-4">
              <code className="min-w-0 flex-1 truncate font-mono text-meta">{k}</code>
              <StandbyGo
                small
                standby="Delete"
                go="Go: delete"
                onGo={() => void api.deleteEnv(name, k).then(onChanged, setError)}
              />
            </li>
          ))}
        </ul>
      )}
      <form onSubmit={submit} className="flex flex-col gap-3 p-4">
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-2">
          <Field label="Key">
            <TextInput
              value={key}
              onChange={(e) => setKey(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, '_'))}
              placeholder="API_KEY"
              required
              className="font-mono"
            />
          </Field>
          <Field label="Value">
            <TextInput
              type="password"
              autoComplete="off"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              className="font-mono"
            />
          </Field>
        </div>
        <ErrorNote error={error} />
        <div className="flex items-center justify-between gap-3">
          <span className="text-meta text-ink-2">Stored encrypted, never shown again.</span>
          <Button type="submit" busy={busy}>
            Set value
          </Button>
        </div>
      </form>
    </Sheet>
  );
}

function Domains({ detail, onChanged }: { detail: ProjectDetail; onChanged: () => void }) {
  const services = detail.services.filter((s) => s.port);
  const [host, setHost] = useState('');
  const [service, setService] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [added, setAdded] = useState<string | null>(null);

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const d = await api.addDomain(detail.name, host.trim(), service || services[0].name);
      setAdded(d.hostname);
      setHost('');
      onChanged();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Sheet quiet id="domains" title="Domains">
      {detail.domains.length > 0 && (
        <ul>
          {detail.domains.map((d) => (
            <li key={d.hostname} className="flex min-h-11 items-center gap-2 border-b border-rule px-4">
              <a
                href={`https://${d.hostname}`}
                target="_blank"
                rel="noreferrer"
                className="min-w-0 flex-1 truncate font-mono text-meta text-ink underline decoration-rule hover:decoration-ink"
              >
                {d.hostname}
              </a>
              <span className="text-meta text-ink-2">{d.service}</span>
              <StandbyGo
                small
                standby="Remove"
                go="Go: remove"
                onGo={() => void api.removeDomain(detail.name, d.hostname).then(onChanged, setError)}
              />
            </li>
          ))}
        </ul>
      )}
      {services.length === 0 ? (
        <p className="p-4 text-meta text-ink-2">Domains point at a service with a port. Deploy one first.</p>
      ) : (
        <form onSubmit={submit} className="flex flex-col gap-3 p-4">
          <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
            <Field label="Hostname">
              <TextInput
                value={host}
                onChange={(e) => setHost(e.target.value)}
                placeholder="app.example.com"
                required
                className="font-mono"
              />
            </Field>
            <Field label="Service">
              <Select value={service || services[0].name} onChange={(e) => setService(e.target.value)}>
                {services.map((s) => (
                  <option key={s.name}>{s.name}</option>
                ))}
              </Select>
            </Field>
          </div>
          <ErrorNote error={error} />
          {added && (
            <p className="text-meta text-ink-2" aria-live="polite">
              Point the A record of <span className="font-mono text-ink">{added}</span> at this server. HTTPS follows
              once DNS resolves.
            </p>
          )}
          <div className="flex justify-end">
            <Button type="submit" busy={busy}>
              Add domain
            </Button>
          </div>
        </form>
      )}
    </Sheet>
  );
}

/** "20261017T154007Z-before-r3.dump" → "before r3". */
function backupReason(b: Backup) {
  const m = /-([a-z0-9-]+)\.dump$/.exec(b.name);
  return m ? m[1].replace('-', ' ') : '';
}

const SHOWN_BACKUPS = 5;

function Backups({ name }: { name: string }) {
  const [list, setList] = useState<Backup[] | null>(null);
  const [showAll, setShowAll] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const load = useCallback(() => api.backups(name).then(setList, setError), [name]);
  useEffect(() => {
    void load();
  }, [load]);

  return (
    <Sheet
      quiet
      id="backups"
      title="Database backups"
      action={
        <Button
          busy={busy}
          className="h-8 px-2.5 text-meta"
          onClick={async () => {
            setBusy(true);
            setError(null);
            try {
              await api.createBackup(name);
              await load();
            } catch (e) {
              setError(e);
            } finally {
              setBusy(false);
            }
          }}
        >
          Back up now
        </Button>
      }
    >
      {error ? (
        <div className="p-4">
          <ErrorNote error={error} />
        </div>
      ) : null}
      {list && list.length === 0 && (
        <p className="p-4 text-meta text-ink-2">Kayly backs up Postgres daily and before every release step.</p>
      )}
      {list && list.length > 0 && (
        <ul>
          {(showAll ? list : list.slice(0, SHOWN_BACKUPS)).map((b) => (
            <li key={b.name} className="flex min-h-11 items-center gap-3 border-b border-rule pr-2.5 pl-4 last:border-b-0">
              <span className="min-w-0 flex-1">
                <span className="block text-body">
                  <When at={b.created_at} />
                </span>
                <span className="flex gap-3 text-meta text-ink-2">
                  <span>{backupReason(b)}</span>
                  <span>{formatBytes(b.size)}</span>
                </span>
              </span>
              <a
                href={api.backupURL(name, b.name)}
                download
                aria-label={`Download backup from ${new Date(b.created_at).toLocaleString()}`}
                title="Download"
                className="inline-flex size-8 items-center justify-center rounded-md text-ink-2 hover:bg-hover hover:text-ink"
              >
                <Download size={16} />
              </a>
            </li>
          ))}
        </ul>
      )}
      {list && list.length > SHOWN_BACKUPS && (
        <div className="px-4 py-2">
          <button
            type="button"
            onClick={() => setShowAll((v) => !v)}
            aria-expanded={showAll}
            className="h-8 rounded-md text-meta font-semibold text-ink underline decoration-rule underline-offset-4 hover:decoration-ink"
          >
            {showAll ? 'Show the newest' : `Show all (${list.length})`}
          </button>
        </div>
      )}
    </Sheet>
  );
}

function PageSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading project" className="flex flex-col gap-5">
      <div className="h-16 w-72 rounded-lg bg-rule/60 motion-safe:animate-pulse" />
      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(320px,400px)]">
        <div className="h-72 rounded-lg border border-rule bg-sheet" />
        <div className="h-72 rounded-lg border border-rule bg-sheet" />
      </div>
    </div>
  );
}
