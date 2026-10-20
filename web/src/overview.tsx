import { ExternalLink } from 'lucide-react';
import { type ReactNode, useEffect, useRef, useState } from 'react';
import { api, type ProjectDetail, type Release, type Service } from '@/api';
import { ArmButton } from '@/components/arm-button';
import { CommandLine } from '@/components/copy';
import { Empty, ErrorNote, Inline, Panel, When } from '@/components/common';
import { ReleaseTrack } from '@/components/release-track';
import { StatusBadge, StatusDot } from '@/components/status';
import { Button } from '@/components/ui/button';
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { formatBytes, releaseStatus, serviceStatus, shortCommit } from '@/lib/format';
import { cn } from '@/lib/utils';
import type { Operation } from '@/project';

/** Overview: what is live, how it got there, and what runs it. */
export function Overview({
  detail,
  op,
  landed,
  onOpen,
  onDismiss,
  onSettings,
}: {
  detail: ProjectDetail;
  op: Operation | null;
  landed: number | null;
  onOpen: (seq: number) => void;
  onDismiss: () => void;
  onSettings: () => void;
}) {
  if (detail.releases.length === 0 && !op) return <FirstDeploy detail={detail} onSettings={onSettings} />;
  // Grid: 3 columns with a 24px gap; releases span 2, services and add-ons 1 (DESIGN.md, Layout).
  return (
    <div className="flex flex-col gap-6">
      {op && <OperationPanel op={op} onDismiss={onDismiss} />}
      <LivePanel detail={detail} landed={landed} onOpen={onOpen} />
      <div className="grid items-start gap-6 lg:grid-cols-3">
        <Releases releases={detail.releases} onOpen={onOpen} className="lg:col-span-2" />
        <div className="flex flex-col gap-6">
          <Services detail={detail} />
          <Attached detail={detail} onSettings={onSettings} />
        </div>
      </div>
    </div>
  );
}

/** A project with no release: the command that deploys it, beside what is already set up for it. */
function FirstDeploy({ detail, onSettings }: { detail: ProjectDetail; onSettings: () => void }) {
  const ready: { label: string; value: string; done: boolean }[] = [
    {
      label: 'Add-ons',
      value: detail.addons.length ? detail.addons.map((a) => (a.kind === 'postgres' ? 'Postgres' : 'Redis')).join(', ') : 'none yet',
      done: detail.addons.length > 0,
    },
    {
      label: 'Env values',
      value: detail.env_keys.length ? `${detail.env_keys.length} set` : 'none yet',
      done: detail.env_keys.length > 0,
    },
    { label: 'Custom domains', value: detail.domains.length ? `${detail.domains.length}` : 'after the first deploy', done: detail.domains.length > 0 },
  ];
  return (
    <section aria-labelledby="first-deploy" className="grid rounded-card border lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]">
      <div className="flex flex-col gap-4 p-5">
        <h2 id="first-deploy" className="text-section font-semibold">
          First deploy
        </h2>
        <p className="max-w-[62ch] text-body text-muted-foreground">
          Run it from the app's folder, next to its <code className="font-mono text-foreground">kayly.toml</code>. Kayly builds it,
          runs the release step, waits for the health check and only then routes traffic. It lists any env value still missing.
        </p>
        <CommandLine>{`kayly deploy -p ${detail.name}`}</CommandLine>
        <p className="text-small text-muted-foreground">
          Or follow a repository so a push deploys: <code className="font-mono">kayly source set git@github.com:you/app.git</code>
        </p>
      </div>
      <div className="flex flex-col border-t lg:border-t-0 lg:border-l">
        <h3 className="px-5 pt-4 pb-2 text-body font-semibold">Ready for it</h3>
        <dl className="flex-1">
          {ready.map((r) => (
            <div key={r.label} className="flex items-center gap-2.5 border-b border-rule px-5 py-3 text-body last:border-b-0">
              <StatusDot tone={r.done ? 'positive' : 'off'} label={r.done ? 'Set up' : 'Not set up'} />
              <dt className="flex-1">{r.label}</dt>
              <dd className="text-muted-foreground">{r.value}</dd>
            </div>
          ))}
        </dl>
        <div className="border-t px-5 py-3">
          <Button variant="outline" size="sm" onClick={onSettings}>
            Set up in Settings
          </Button>
        </div>
      </div>
    </section>
  );
}

/** The release serving traffic, as the hero: its commit, its place in the track, its facts. */
function LivePanel({ detail, landed, onOpen }: { detail: ProjectDetail; landed: number | null; onOpen: (seq: number) => void }) {
  const live = detail.releases.find((r) => r.status === 'live');
  const url = detail.services.find((s) => s.urls.length > 0)?.urls[0];
  const running = detail.services.filter((s) => s.state === 'running').length;
  const kept = detail.releases.filter((r) => r.can_roll_back).map((r) => `r${r.seq}`);
  const facts: { label: string; value: ReactNode }[] = [
    {
      label: 'URL',
      value: url ? (
        <a href={url} target="_blank" rel="noreferrer" className="inline-flex min-w-0 items-center gap-1 font-mono hover:underline">
          <span className="truncate">{url.replace(/^https:\/\//, '')}</span>
          <ExternalLink className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
        </a>
      ) : (
        <span className="text-muted-foreground">no port</span>
      ),
    },
    {
      label: 'Services',
      value: detail.services.length ? (
        <span className={cn('tabular', running < detail.services.length && 'text-warning')}>
          {running} of {detail.services.length} running
        </span>
      ) : (
        <span className="text-muted-foreground">—</span>
      ),
    },
    { label: 'Roll back to', value: kept.length ? <span className="font-mono">{kept.slice(0, 3).join(', ')}</span> : <span className="text-muted-foreground">nothing kept</span> },
  ];

  return (
    <section aria-labelledby="live" className="grid rounded-card border lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]">
      <div className="flex min-w-0 flex-col gap-4 p-5">
        <div className="flex min-w-0 flex-col gap-1">
          <h2 id="live" className="truncate text-section font-semibold">
            {live ? live.commit_message || `Release r${live.seq}` : 'Nothing live'}
          </h2>
          {live && (
            <p className="flex flex-wrap items-center gap-x-2 text-body text-muted-foreground">
              <span className="font-mono text-foreground">r{live.seq}</span>
              {live.commit && (
                <>
                  <span aria-hidden="true">·</span>
                  <span className="font-mono">{shortCommit(live.commit)}</span>
                  {live.commit.endsWith('-dirty') && <span>with uncommitted changes</span>}
                </>
              )}
              <span aria-hidden="true">·</span>
              <span>
                live since <When at={live.created_at} />
              </span>
            </p>
          )}
        </div>
        <ReleaseTrack releases={detail.releases} landed={landed} onOpen={onOpen} />
      </div>
      <dl className="grid border-t lg:border-t-0 lg:border-l">
        {facts.map((f) => (
          <div key={f.label} className="flex min-w-0 items-center justify-between gap-4 border-b border-rule px-5 py-3 text-body last:border-b-0">
            <dt className="text-muted-foreground">{f.label}</dt>
            <dd className="flex min-w-0 justify-end">{f.value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

const ROWS = 6;

function Releases({ releases, onOpen, className }: { releases: Release[]; onOpen: (seq: number) => void; className?: string }) {
  const [all, setAll] = useState(false);
  const shown = all ? releases : releases.slice(0, ROWS);
  return (
    <Panel
      id="releases"
      title="Releases"
      meta={`${releases.length} in total`}
      className={className}
      action={
        releases.length > ROWS && (
          <Button variant="ghost" size="sm" onClick={() => setAll((v) => !v)}>
            {all ? 'Show recent' : `Show all ${releases.length}`}
          </Button>
        )
      }
    >
      <ul>
        {shown.map((r) => (
          <li key={r.seq} className="border-b border-rule last:border-b-0">
            <button
              type="button"
              onClick={() => onOpen(r.seq)}
              className="grid min-h-12 w-full grid-cols-[3rem_6.5rem_minmax(0,1fr)_auto] items-center gap-3 px-4 py-2 text-left transition-colors duration-150 hover:bg-raised max-sm:grid-cols-[3rem_minmax(0,1fr)_auto]"
            >
              <span className="font-mono font-medium">r{r.seq}</span>
              <span className="max-sm:hidden">
                <StatusBadge tone={releaseStatus[r.status].tone}>{releaseStatus[r.status].word}</StatusBadge>
              </span>
              <span className="flex min-w-0 flex-col">
                <span className="truncate text-body">
                  {r.status === 'failed' && r.error ? (
                    <span className="text-negative">{r.error.split('\n')[0].replaceAll('`', '')}</span>
                  ) : (
                    r.commit_message || <span className="text-muted-foreground">No commit message</span>
                  )}
                </span>
                <span className="truncate text-small text-muted-foreground">
                  <span className="sm:hidden">{releaseStatus[r.status].word} · </span>
                  {r.commit ? <span className="font-mono">{shortCommit(r.commit)}</span> : 'uploaded working tree'}
                  {r.status === 'failed' && r.commit_message && <> · {r.commit_message}</>}
                </span>
              </span>
              <span className="text-right text-small whitespace-nowrap text-muted-foreground">
                <When at={r.created_at} />
              </span>
            </button>
          </li>
        ))}
      </ul>
    </Panel>
  );
}

const POLL_MS = 10_000;

/** The live release's services, refreshed with CPU and memory every ten seconds. */
function useServices(detail: ProjectDetail): Service[] | null {
  const [fresh, setFresh] = useState<Service[] | null>(null);
  const name = detail.name;
  useEffect(() => {
    let stop = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      try {
        const svcs = await api.services(name);
        if (!stop) setFresh(svcs);
      } catch {
        // keep the last answer; the project view already reports a broken API
      }
      if (!stop) timer = setTimeout(tick, POLL_MS);
    };
    void tick();
    return () => {
      stop = true;
      clearTimeout(timer);
    };
  }, [name, detail.live_release]);
  return fresh;
}

function Services({ detail }: { detail: ProjectDetail }) {
  const fresh = useServices(detail);
  const services = fresh ?? detail.services;
  return (
    <Panel id="services" title="Services" meta={services.length ? `${services.length}` : undefined}>
      {services.length === 0 ? (
        <Empty>Services start with the first live release.</Empty>
      ) : (
        <ul>
          {services.map((s) => {
            const state = serviceStatus[s.state];
            return (
              <li key={s.name} className="flex flex-col gap-1 border-b border-rule px-4 py-3 last:border-b-0">
                <span className="flex items-center gap-2">
                  <StatusDot tone={state.tone} label={state.word} />
                  <span className="font-medium">{s.name}</span>
                  <span className="text-small text-muted-foreground">{s.port ? `:${s.port}` : 'no port'}</span>
                  <span className="ml-auto flex items-center gap-1.5">
                    {s.state !== 'running' && <StatusBadge tone={state.tone}>{state.word}</StatusBadge>}
                    {s.restarts > 0 && (
                      <StatusBadge tone="warning">{`${s.restarts} restart${s.restarts === 1 ? '' : 's'}`}</StatusBadge>
                    )}
                  </span>
                </span>
                {s.stats ? (
                  <span className="tabular text-small text-muted-foreground">
                    {s.stats.cpu_percent.toFixed(1)}% CPU · {formatBytes(s.stats.memory_bytes)}
                    {s.memory_limit ? <> of {formatBytes(s.memory_limit)}</> : null}
                  </span>
                ) : fresh === null && s.state === 'running' ? (
                  <Skeleton className="my-0.5 h-3 w-36" />
                ) : null}
                {s.urls[0] ? (
                  <a href={s.urls[0]} target="_blank" rel="noreferrer" className="truncate font-mono text-small hover:underline">
                    {s.urls[0].replace(/^https:\/\//, '')}
                  </a>
                ) : (
                  s.command && <code className="truncate font-mono text-small text-muted-foreground">{s.command}</code>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}

/** What the project has from Kayly besides its releases: add-ons and custom domains. */
function Attached({ detail, onSettings }: { detail: ProjectDetail; onSettings: () => void }) {
  return (
    <Panel
      id="attached"
      title="Add-ons and domains"
      action={
        <Button variant="ghost" size="sm" onClick={onSettings}>
          Settings
        </Button>
      }
    >
      <ul>
        {detail.addons.map((a) => (
          <li key={a.kind} className="flex min-h-11 items-center gap-2 border-b border-rule px-4 text-body">
            <StatusDot tone={a.status === 'healthy' ? 'positive' : a.status === 'starting' ? 'warning' : 'negative'} label={a.status} />
            <span className="font-medium">{a.kind === 'postgres' ? 'Postgres' : 'Redis'}</span>
            <span className="ml-auto font-mono text-small text-muted-foreground">{a.env_var}</span>
          </li>
        ))}
        {detail.domains.map((d) => (
          <li key={d.hostname} className="flex min-h-11 items-center gap-2 border-b border-rule px-4 text-body">
            <span className="min-w-0 flex-1 truncate font-mono">{d.hostname}</span>
            <span className="text-small text-muted-foreground">{d.service}</span>
          </li>
        ))}
        <li className="px-4 py-3 text-small text-muted-foreground">
          {detail.addons.length === 0 && detail.domains.length === 0
            ? 'No add-ons or custom domains yet.'
            : `${detail.env_keys.length} env value${detail.env_keys.length === 1 ? '' : 's'} set`}
        </li>
      </ul>
    </Panel>
  );
}

/** Live output of a redeploy or rollback while it runs, and its outcome after. */
function OperationPanel({ op, onDismiss }: { op: Operation; onDismiss: () => void }) {
  const ref = useRef<HTMLPreElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [op.log]);
  const tone = !op.result ? 'warning' : op.result.ok ? 'positive' : 'negative';
  const title = !op.result
    ? `${op.title}: in progress`
    : op.result.ok
      ? `${op.title}: r${op.result.release} is live`
      : op.result.unknown
        ? `${op.title}: result unknown`
        : `${op.title} failed. The live release kept serving.`;
  return (
    <section aria-labelledby="operation" className="flex flex-col rounded-card border">
      <header className="flex min-h-12 items-center gap-3 border-b px-4">
        <StatusDot tone={tone} />
        <h2 id="operation" aria-live="polite" className="min-w-0 flex-1 text-body font-semibold">
          {title}
        </h2>
        {op.result && (
          <Button variant="ghost" size="sm" onClick={onDismiss}>
            Close
          </Button>
        )}
      </header>
      {op.result && !op.result.ok && op.result.error && (
        <p className="border-b px-4 py-2.5 text-body text-negative">
          <Inline text={op.result.error} />
        </p>
      )}
      <pre
        ref={ref}
        tabIndex={0}
        aria-label={`${op.title} output`}
        className="max-h-64 min-h-24 overflow-auto rounded-b-card bg-raised px-4 py-3 font-mono text-small whitespace-pre-wrap"
      >
        {op.log.replace(/^\n+/, '') || 'Waiting for the first line…'}
      </pre>
    </section>
  );
}

/** One release in a side sheet: what it was built from, why it failed, its deploy log, rollback. */
export function ReleaseSheet({
  project,
  release: r,
  live,
  busy,
  onClose,
  onRollback,
}: {
  project: string;
  release?: Release;
  live?: Release;
  busy: boolean;
  onClose: () => void;
  onRollback: (seq: number) => void;
}) {
  return (
    <Sheet open={Boolean(r)} onOpenChange={(o) => !o && onClose()}>
      <SheetContent>
        {r && (
          <>
            <div className="flex flex-col gap-2 border-b px-5 pt-5 pb-4">
              <div className="flex items-center gap-3 pr-10">
                <SheetTitle className="font-mono text-title">r{r.seq}</SheetTitle>
                <StatusBadge tone={releaseStatus[r.status].tone}>{releaseStatus[r.status].word}</StatusBadge>
              </div>
              <SheetDescription>
                {r.commit_message || 'Uploaded from a working tree'} · <When at={r.created_at} />
              </SheetDescription>
            </div>
            <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto p-5">
              <dl className="grid grid-cols-[7rem_minmax(0,1fr)] gap-y-2 text-body">
                <dt className="text-muted-foreground">Created</dt>
                <dd className="tabular">{new Date(r.created_at).toLocaleString()}</dd>
                <dt className="text-muted-foreground">Commit</dt>
                <dd>
                  {r.commit ? (
                    <>
                      <span className="font-mono">{shortCommit(r.commit)}</span>
                      {r.commit.endsWith('-dirty') && <span className="text-muted-foreground"> with uncommitted changes</span>}
                    </>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </dd>
              </dl>

              {r.error && <FailureReason error={r.error} />}
              {r.status === 'failed' && (
                <p className="text-body text-muted-foreground">
                  {live ? `The live release, r${live.seq}, kept serving.` : 'Nothing was live, so nothing changed.'}
                </p>
              )}
              {r.status === 'live' && <p className="text-body text-muted-foreground">Serving now. Redeploy runs this image again.</p>}
              {r.status === 'superseded' &&
                (r.can_roll_back ? (
                  <div className="flex flex-col items-start gap-2">
                    <ArmButton label={`Roll back to r${r.seq}`} go={`Go: r${r.seq} live`} busy={busy} onGo={() => !busy && onRollback(r.seq)} />
                    <p className="text-small text-muted-foreground">Starts this image with its own env next to the live release, then switches.</p>
                  </div>
                ) : (
                  <p className="text-body text-muted-foreground">Its image was pruned (Kayly keeps the last three), so it cannot be rolled back to.</p>
                ))}

              <DeployLog key={r.seq} project={project} seq={r.seq} />
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}

function FailureReason({ error }: { error: string }) {
  const [first, ...rest] = error.split('\n');
  return (
    <div className="flex flex-col gap-2 rounded-card bg-pill-negative p-3 text-pill-negative-ink">
      <p className="text-body font-medium">
        <Inline text={first} />
      </p>
      {rest.length > 0 && <pre className="max-h-48 overflow-auto font-mono text-small whitespace-pre-wrap">{rest.join('\n')}</pre>}
    </div>
  );
}

/** The output of the deploy that made a release. */
function DeployLog({ project, seq }: { project: string; seq: number }) {
  const [log, setLog] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    api.releaseLog(project, seq).then(setLog, setError);
  }, [project, seq]);
  return (
    <section aria-labelledby="deploy-log" className="flex flex-col gap-2">
      <h3 id="deploy-log" className="text-body font-semibold">
        Deploy log
      </h3>
      {error ? (
        <ErrorNote error={error} />
      ) : log === null ? (
        <Skeleton className="h-32 rounded-card" />
      ) : (
        <pre tabIndex={0} aria-label={`Deploy log of r${seq}`} className="max-h-[50vh] overflow-auto rounded-card bg-raised p-3 font-mono text-small whitespace-pre-wrap">
          {log.replace(/^\n+/, '') || 'The deploy wrote nothing.'}
        </pre>
      )}
    </section>
  );
}
