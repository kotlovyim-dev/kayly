import { Download, Trash2 } from 'lucide-react';
import { type SubmitEvent, useCallback, useEffect, useState } from 'react';
import { api, type Addon, type AddonKind, type Backup, type ProjectDetail } from './api';
import {
  ArmGo,
  Button,
  CopyButton,
  ErrorNote,
  IconButton,
  Lamp,
  type LampState,
  Pane,
  Select,
  TextInput,
  When,
  formatBytes,
} from './ui';

/** Settings: everything Kayly keeps for the project, on one screen. */
export function Settings({
  detail,
  onChanged,
  onDomainsChanged,
}: {
  detail: ProjectDetail;
  /** Add-ons and env values changed: they reach services on the next deploy. */
  onChanged: () => void;
  onDomainsChanged: () => void;
}) {
  const postgres = detail.addons.some((a) => a.kind === 'postgres');
  return (
    <div className="grid gap-3 sm:gap-5 lg:h-full lg:grid-cols-2">
      <div className="flex min-h-0 flex-col gap-3 sm:gap-5">
        <Addons name={detail.name} addons={detail.addons} onChanged={onChanged} />
        <Env name={detail.name} keys={detail.env_keys} onChanged={onChanged} />
      </div>
      <div className="flex min-h-0 flex-col gap-3 sm:gap-5">
        <Domains detail={detail} onChanged={onDomainsChanged} className={postgres ? 'max-lg:shrink-0 lg:max-h-[45%]' : 'max-lg:shrink-0 lg:flex-1'} />
        {postgres && <Backups name={detail.name} />}
      </div>
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
    <Pane id="addons" title="Add-ons" className="shrink-0">
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
    </Pane>
  );
}

function AvailableAddon({ name, kind, onChanged }: { name: string; kind: AddonKind; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const info = addonInfo[kind];
  return (
    <div className="flex flex-col gap-2 px-4 py-2.5">
      <div className="flex items-center gap-3">
        <Lamp state="off" />
        <div className="min-w-0 flex-1">
          <p className="font-semibold">{info.label}</p>
          <p className="text-meta text-ink-2">
            Services get <code className="font-mono">{info.gives}</code>
          </p>
        </div>
        <Button
          small
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
    <div className="flex flex-col gap-3 px-4 py-2.5">
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
        <form onSubmit={remove} className="flex flex-col gap-2 rounded-[3px] border border-fail/50 p-3">
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
    <Pane id="env" title="Env values" className="max-lg:shrink-0 lg:flex-1">
      <form onSubmit={submit} className="flex flex-col gap-2 border-b border-rule p-3">
        <div className="flex gap-2">
          <label className="min-w-0 flex-1">
            <span className="sr-only">Key</span>
            <TextInput
              value={key}
              onChange={(e) => setKey(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, '_'))}
              placeholder="KEY"
              required
              className="font-mono"
            />
          </label>
          <label className="min-w-0 flex-1">
            <span className="sr-only">Value</span>
            <TextInput
              type="password"
              autoComplete="off"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder="value"
              className="font-mono"
            />
          </label>
          <Button type="submit" busy={busy}>
            Set
          </Button>
        </div>
        <ErrorNote error={error} />
        <span className="text-meta text-ink-2">Stored encrypted and never shown again. Applies on the next deploy.</span>
      </form>
      {keys.length === 0 ? (
        <p className="px-4 py-3 text-meta text-ink-2">No values yet.</p>
      ) : (
        <ul>
          {keys.map((k) => (
            <li key={k} className="flex min-h-11 items-center gap-2 border-b border-rule pr-3 pl-4 last:border-b-0">
              <code className="min-w-0 flex-1 truncate font-mono text-meta">{k}</code>
              <ArmGo small label="Delete" go="Go: delete" onGo={() => void api.deleteEnv(name, k).then(onChanged, setError)} />
            </li>
          ))}
        </ul>
      )}
    </Pane>
  );
}

function Domains({ detail, onChanged, className }: { detail: ProjectDetail; onChanged: () => void; className: string }) {
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
    <Pane id="domains" title="Domains" className={className}>
      {services.length === 0 ? (
        <p className="px-4 py-3 text-meta text-ink-2">Domains point at a service with a port. Deploy one first.</p>
      ) : (
        <form onSubmit={submit} className="flex flex-col gap-2 border-b border-rule p-3">
          <div className="flex gap-2">
            <label className="min-w-0 flex-1">
              <span className="sr-only">Hostname</span>
              <TextInput
                value={host}
                onChange={(e) => setHost(e.target.value)}
                placeholder="app.example.com"
                required
                className="font-mono"
              />
            </label>
            {services.length > 1 && (
              <label className="w-32 shrink-0">
                <span className="sr-only">Service</span>
                <Select value={service || services[0].name} onChange={(e) => setService(e.target.value)}>
                  {services.map((s) => (
                    <option key={s.name}>{s.name}</option>
                  ))}
                </Select>
              </label>
            )}
            <Button type="submit" busy={busy}>
              Add
            </Button>
          </div>
          <ErrorNote error={error} />
          <span className="text-meta text-ink-2" aria-live="polite">
            {added ? (
              <>
                Point the A record of <span className="font-mono text-ink">{added}</span> at this server. HTTPS follows
                once DNS resolves.
              </>
            ) : (
              'Needs an A record pointing at this server.'
            )}
          </span>
        </form>
      )}
      {detail.domains.length > 0 && (
        <ul>
          {detail.domains.map((d) => (
            <li key={d.hostname} className="flex min-h-11 items-center gap-2 border-b border-rule pr-3 pl-4 last:border-b-0">
              <a
                href={`https://${d.hostname}`}
                target="_blank"
                rel="noreferrer"
                className="min-w-0 flex-1 truncate font-mono text-meta text-ink underline decoration-rule hover:decoration-ink"
              >
                {d.hostname}
              </a>
              <span className="text-meta text-ink-2">{d.service}</span>
              <ArmGo
                small
                label="Remove"
                go="Go: remove"
                onGo={() => void api.removeDomain(detail.name, d.hostname).then(onChanged, setError)}
              />
            </li>
          ))}
        </ul>
      )}
    </Pane>
  );
}

/** "20261017T154007Z-before-r3.dump" → "before r3". */
function backupReason(b: Backup) {
  const m = /-([a-z0-9-]+)\.dump$/.exec(b.name);
  return m ? m[1].replace('-', ' ') : '';
}

function Backups({ name }: { name: string }) {
  const [list, setList] = useState<Backup[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const load = useCallback(() => api.backups(name).then(setList, setError), [name]);
  useEffect(() => {
    void load();
  }, [load]);

  return (
    <Pane
      id="backups"
      title={list && list.length > 0 ? `Database backups · ${list.length}` : 'Database backups'}
      className="lg:flex-1"
      action={
        <Button
          small
          busy={busy}
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
        <div className="px-4 py-3">
          <ErrorNote error={error} />
        </div>
      ) : null}
      {list && list.length === 0 && (
        <p className="px-4 py-3 text-meta text-ink-2">Kayly backs up Postgres daily and before every release step.</p>
      )}
      {list && list.length > 0 && (
        <ul>
          {list.map((b) => (
            <li key={b.name} className="flex min-h-11 items-center gap-3 border-b border-rule pr-3 pl-4 last:border-b-0">
              <span className="min-w-0 flex-1">
                <When at={b.created_at} />
              </span>
              <span className="font-mono text-meta text-ink-2">{backupReason(b)}</span>
              <span className="w-16 text-right text-meta text-ink-2 tabular-nums">{formatBytes(b.size)}</span>
              <a
                href={api.backupURL(name, b.name)}
                download
                aria-label={`Download backup from ${new Date(b.created_at).toLocaleString()}`}
                title="Download"
                className="inline-flex size-8 items-center justify-center rounded-[3px] text-ink-2 hover:bg-hover hover:text-ink"
              >
                <Download size={16} />
              </a>
            </li>
          ))}
        </ul>
      )}
    </Pane>
  );
}
