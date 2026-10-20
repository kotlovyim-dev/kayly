import { Download, Trash2 } from 'lucide-react';
import { type FormEvent, type ReactNode, useCallback, useEffect, useState } from 'react';
import { api, type Addon, type AddonKind, type Backup, type ProjectDetail } from '@/api';
import { ArmButton } from '@/components/arm-button';
import { CopyButton } from '@/components/copy';
import { ErrorNote, When } from '@/components/common';
import { StatusBadge } from '@/components/status';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { backupLabel, formatBytes } from '@/lib/format';

/** Settings: everything Kayly keeps for the project, one section per row. */
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
  return (
    <div className="flex flex-col">
      <Section
        id="addons"
        title="Add-ons"
        description="A database next to your services, on the project's own network. Its URL reaches every service on the next deploy."
      >
        <Addons name={detail.name} addons={detail.addons} onChanged={onChanged} />
      </Section>
      <Section
        id="env"
        title="Env values"
        description={
          <>
            Values for the keys declared in <code className="font-mono">kayly.toml</code>. Stored encrypted, never shown again, and
            delivered on the next deploy only to the services each key is scoped to.
          </>
        }
      >
        <Env name={detail.name} keys={detail.env_keys} onChanged={onChanged} />
      </Section>
      <Section
        id="domains"
        title="Domains"
        description="Every service with a port already has an sslip.io address. A custom domain needs an A record pointing at this server; HTTPS follows once DNS resolves."
      >
        <Domains detail={detail} onChanged={onDomainsChanged} />
      </Section>
      {detail.addons.length > 0 && (
        <Section
          id="backups"
          title="Backups"
          description="Postgres before every release step and daily; Redis and volumes daily. The last 14 of each are kept on this server."
        >
          <Backups name={detail.name} />
        </Section>
      )}
    </div>
  );
}

function Section({ id, title, description, children }: { id: string; title: string; description: ReactNode; children: ReactNode }) {
  return (
    <section aria-labelledby={id} className="grid gap-4 border-b py-8 first:pt-2 last:border-b-0 md:grid-cols-[minmax(0,18rem)_minmax(0,1fr)] md:gap-10">
      <div className="flex flex-col gap-1.5 md:pt-1.5">
        <h2 id={id} className="text-section font-medium">
          {title}
        </h2>
        <p className="text-body text-muted-foreground">{description}</p>
      </div>
      <div className="flex min-w-0 flex-col gap-4 rounded-sheet bg-raised p-4">{children}</div>
    </section>
  );
}

/** A list inside a settings panel: white rows on the panel's tint. */
function Rows({ children }: { children: ReactNode }) {
  return <ul className="flex flex-col overflow-hidden rounded-card border bg-background">{children}</ul>;
}

const row = 'flex min-h-12 items-center gap-3 border-b border-rule px-3 last:border-b-0';

const addonInfo: Record<AddonKind, { label: string; gives: string }> = {
  postgres: { label: 'Postgres', gives: 'DATABASE_URL' },
  redis: { label: 'Redis', gives: 'REDIS_URL' },
};

function Addons({ name, addons, onChanged }: { name: string; addons: Addon[]; onChanged: () => void }) {
  return (
    <Rows>
      {(['postgres', 'redis'] as const).map((kind) => {
        const a = addons.find((x) => x.kind === kind);
        return a ? (
          <AttachedAddon key={kind} name={name} addon={a} onChanged={onChanged} />
        ) : (
          <AvailableAddon key={kind} name={name} kind={kind} onChanged={onChanged} />
        );
      })}
    </Rows>
  );
}

function AvailableAddon({ name, kind, onChanged }: { name: string; kind: AddonKind; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const info = addonInfo[kind];
  return (
    <li className="flex flex-col gap-2 border-b border-rule px-3 py-2.5 last:border-b-0">
      <div className="flex items-center gap-3">
        <div className="min-w-0 flex-1">
          <p className="font-medium">{info.label}</p>
          <p className="text-small text-muted-foreground">
            Services get <code className="font-mono">{info.gives}</code>
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
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
    </li>
  );
}

function AttachedAddon({ name, addon, onChanged }: { name: string; addon: Addon; onChanged: () => void }) {
  const [removing, setRemoving] = useState(false);
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const info = addonInfo[addon.kind];
  const tone = addon.status === 'healthy' ? 'positive' : addon.status === 'starting' ? 'warning' : 'negative';

  async function remove(e: FormEvent) {
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
    <li className="flex flex-col gap-3 border-b border-rule px-3 py-2.5 last:border-b-0">
      <div className="flex items-center gap-3">
        <div className="min-w-0 flex-1">
          <p className="flex items-center gap-2 font-medium">
            {info.label}
            <StatusBadge tone={tone}>{addon.status[0].toUpperCase() + addon.status.slice(1)}</StatusBadge>
          </p>
          <p className="text-small text-muted-foreground" title={addon.image}>
            <code className="font-mono">{addon.env_var}</code> · since <When at={addon.created_at} />
          </p>
        </div>
        {addon.url && <CopyButton text={addon.url} label={`Copy ${addon.env_var}`} />}
        <Button
          variant="ghost"
          size="icon"
          aria-label={`Remove ${info.label}`}
          title={`Remove ${info.label}`}
          aria-expanded={removing}
          onClick={() => setRemoving((v) => !v)}
          className="size-7 text-muted-foreground hover:text-negative"
        >
          <Trash2 />
        </Button>
      </div>
      {removing && (
        <form onSubmit={remove} className="flex flex-col gap-2 rounded-card bg-pill-negative p-3">
          <p className="text-body text-pill-negative-ink">
            This deletes {info.label} and all its data. Backups stay. Type <strong className="font-mono">{name}</strong> to confirm.
          </p>
          <div className="flex gap-2">
            <Input
              aria-label="Project name"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              className="font-mono"
              autoFocus
            />
            <Button type="submit" variant="destructive" busy={busy} disabled={confirm !== name}>
              Delete {info.label}
            </Button>
          </div>
          <ErrorNote error={error} />
        </form>
      )}
    </li>
  );
}

function Env({ name, keys, onChanged }: { name: string; keys: string[]; onChanged: () => void }) {
  const [key, setKey] = useState('');
  const [value, setValue] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function submit(e: FormEvent) {
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
    <>
      <form onSubmit={submit} className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-end">
        <div className="flex flex-col gap-2">
          <Label htmlFor="env-key">Key</Label>
          <Input
            id="env-key"
            value={key}
            onChange={(e) => setKey(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, '_'))}
            placeholder="SMTP_URL"
            required
            className="font-mono"
          />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor="env-value">Value</Label>
          <Input id="env-value" type="password" autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)} className="font-mono" />
        </div>
        <Button type="submit" busy={busy} variant="outline" className="h-9">
          Set value
        </Button>
      </form>
      <ErrorNote error={error} />
      {keys.length === 0 ? (
        <p className="text-body text-muted-foreground">No values yet. Kayly lists the keys it still needs when you deploy.</p>
      ) : (
        <Rows>
          {keys.map((k) => (
            <li key={k} className={row}>
              <code className="min-w-0 flex-1 truncate font-mono text-body">{k}</code>
              <ArmButton variant="ghost" size="sm" label="Delete" go={`Go: delete ${k}`} onGo={() => void api.deleteEnv(name, k).then(onChanged, setError)} />
            </li>
          ))}
        </Rows>
      )}
    </>
  );
}

function Domains({ detail, onChanged }: { detail: ProjectDetail; onChanged: () => void }) {
  const services = detail.services.filter((s) => s.port);
  const [host, setHost] = useState('');
  const [service, setService] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [added, setAdded] = useState<string | null>(null);

  async function submit(e: FormEvent) {
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

  if (services.length === 0) {
    return <p className="text-body text-muted-foreground">Domains point at a service with a port. Deploy one first.</p>;
  }
  return (
    <>
      <form onSubmit={submit} className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_9rem_auto] sm:items-end">
        <div className="flex flex-col gap-2">
          <Label htmlFor="domain-host">Hostname</Label>
          <Input id="domain-host" value={host} onChange={(e) => setHost(e.target.value)} placeholder={`app.${detail.name}.com`} required className="font-mono" />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor="domain-service">Service</Label>
          <Select value={service || services[0].name} onValueChange={setService}>
            <SelectTrigger id="domain-service">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {services.map((s) => (
                <SelectItem key={s.name} value={s.name}>
                  {s.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <Button type="submit" busy={busy} variant="outline" className="h-9">
          Add domain
        </Button>
      </form>
      <ErrorNote error={error} />
      {added && (
        <p className="text-body text-muted-foreground" aria-live="polite">
          Point the A record of <span className="font-mono text-foreground">{added}</span> at this server.
        </p>
      )}
      <Rows>
        {services.flatMap((s) =>
          s.urls
            .filter((u) => u.endsWith('.sslip.io'))
            .map((u) => (
              <li key={u} className={row}>
                <a href={u} target="_blank" rel="noreferrer" className="min-w-0 flex-1 truncate font-mono text-body hover:underline">
                  {u.replace(/^https:\/\//, '')}
                </a>
                <span className="text-small text-muted-foreground">{s.name} · built in</span>
              </li>
            )),
        )}
        {detail.domains.map((d) => (
          <li key={d.hostname} className={row}>
            <a href={`https://${d.hostname}`} target="_blank" rel="noreferrer" className="min-w-0 flex-1 truncate font-mono text-body hover:underline">
              {d.hostname}
            </a>
            <span className="text-small text-muted-foreground">{d.service}</span>
            <ArmButton variant="ghost" size="sm" label="Remove" go={`Go: remove`} onGo={() => void api.removeDomain(detail.name, d.hostname).then(onChanged, setError)} />
          </li>
        ))}
      </Rows>
    </>
  );
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
    <>
      <div className="flex items-center justify-between gap-3">
        <p className="text-body text-muted-foreground">{list ? `${list.length} on this server` : 'Loading…'}</p>
        <Button
          variant="outline"
          size="sm"
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
      </div>
      <ErrorNote error={error} />
      {list === null ? (
        <Skeleton className="h-24 rounded-card" />
      ) : list.length > 0 ? (
        <Rows>
          {list.map((b) => {
            const label = backupLabel(b.name);
            return (
              <li key={b.name} className={row}>
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate">{label ? `${label.what}, ${label.why}` : b.name}</span>
                  <span className="text-small text-muted-foreground">
                    <When at={b.created_at} />
                  </span>
                </span>
                <span className="tabular text-right text-small text-muted-foreground">{formatBytes(b.size)}</span>
                <Button variant="ghost" size="icon" asChild className="size-7 text-muted-foreground hover:text-foreground">
                  <a href={api.backupURL(name, b.name)} download aria-label={`Download backup from ${new Date(b.created_at).toLocaleString()}`} title="Download">
                    <Download />
                  </a>
                </Button>
              </li>
            );
          })}
        </Rows>
      ) : null}
    </>
  );
}
