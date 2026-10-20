import { type MouseEvent, useCallback, useEffect, useState } from 'react';
import { type Navigate, projectPath } from '@/App';
import { ApiError, api, type ProjectDetail, type Release, type StreamResult } from '@/api';
import { ArmButton } from '@/components/arm-button';
import { ErrorNote, Kbd, When } from '@/components/common';
import { StatusBadge } from '@/components/status';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { shortCommit } from '@/lib/format';
import { cn } from '@/lib/utils';
import { Logs } from '@/logs';
import { Overview, ReleaseSheet } from '@/overview';
import { Settings } from '@/settings';

export type Tab = 'overview' | 'logs' | 'settings';

const tabs: { id: Tab; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'logs', label: 'Logs' },
  { id: 'settings', label: 'Settings' },
];

export type Operation = { title: string; log: string; result?: StreamResult };

/** The project's detail, refreshed every few seconds while the tab is visible. */
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
  tab,
  navigate,
  onChanged,
}: {
  name: string;
  tab: Tab;
  navigate: Navigate;
  onChanged: () => Promise<void>;
}) {
  const { detail, error, reload } = useProject(name);
  const [op, setOp] = useState<Operation | null>(null);
  const [landed, setLanded] = useState<number | null>(null);
  const [opened, setOpened] = useState<number | null>(null);
  // Env values and add-ons reach running services only through a new release.
  const [pending, setPending] = useState(false);

  // 1, 2 and 3 switch tabs when the keyboard is not busy with a field or a dialog.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (e.metaKey || e.ctrlKey || e.altKey || t.closest('input, textarea, select, [role=dialog]')) return;
      const next = tabs[Number(e.key) - 1];
      if (next) navigate(projectPath(name, next.id));
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [name, navigate]);

  const run = useCallback(
    async (title: string, start: (onLog: (t: string) => void) => Promise<StreamResult>, rollbackTo?: number) => {
      setOp({ title, log: '' });
      setOpened(null);
      navigate(projectPath(name));
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
        setLanded(result.release ?? null);
        setPending(false);
      }
      await reload();
      await onChanged();
    },
    [reload, onChanged, detail, name, navigate],
  );

  if (error && !detail) {
    return (
      <div className="flex flex-col items-start gap-3 p-6">
        <ErrorNote error={error} />
        <Button variant="outline" onClick={() => navigate('/')}>
          All projects
        </Button>
      </div>
    );
  }
  if (!detail) return <PageSkeleton />;

  const live = detail.releases.find((r) => r.status === 'live');
  const newest = detail.releases[0];
  const failedAfterLive = newest?.status === 'failed' && (!live || newest.seq > live.seq) ? newest : undefined;
  const busy = Boolean(op && !op.result);
  const redeploy = () => void run('Redeploy', (onLog) => api.redeploy(name, onLog));
  const tabClick = (t: Tab) => (e: MouseEvent) => {
    e.preventDefault();
    navigate(projectPath(name, t));
  };

  return (
    <div className="flex min-h-full flex-col">
      <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-3 px-4 pt-5 sm:px-6 sm:pt-6">
        <div className="flex min-w-0 flex-col gap-1">
          <div className="flex items-center gap-3">
            <h1 className="truncate text-title font-semibold">{name}</h1>
            <ProjectState detail={detail} live={live} />
          </div>
          <Finding live={live} failed={failedAfterLive} pending={pending} newest={newest} />
        </div>
        {live && (
          <div className="flex items-center gap-2 max-sm:w-full">
            {failedAfterLive && (
              <Button onClick={() => setOpened(failedAfterLive.seq)} className="max-sm:flex-1">
                Why r{failedAfterLive.seq} failed
              </Button>
            )}
            <ArmButton
              label="Redeploy"
              go={`Go: r${live.seq}'s image again`}
              variant={failedAfterLive ? 'outline' : 'default'}
              busy={busy}
              onGo={redeploy}
              className="max-sm:flex-1"
            />
          </div>
        )}
      </div>

      <nav aria-label="Project" className="mt-4 flex gap-5 overflow-x-auto border-b px-4 sm:px-6">
        {tabs.map((t, i) => (
          <a
            key={t.id}
            href={projectPath(name, t.id)}
            onClick={tabClick(t.id)}
            aria-current={t.id === tab ? 'page' : undefined}
            className={cn(
              'group -mb-px flex h-10 items-center gap-2 border-b-2 text-body transition-colors duration-150',
              t.id === tab ? 'border-foreground font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground',
            )}
          >
            {t.label}
            <Kbd className="h-4 px-1 opacity-0 transition-opacity duration-150 group-hover:opacity-100 max-sm:hidden">{i + 1}</Kbd>
          </a>
        ))}
      </nav>

      <div className="flex min-h-0 flex-1 flex-col p-4 sm:p-6">
        {tab === 'overview' && (
          <Overview
            detail={detail}
            op={op}
            landed={landed}
            onOpen={setOpened}
            onDismiss={() => setOp(null)}
            onSettings={() => navigate(projectPath(name, 'settings'))}
          />
        )}
        {tab === 'logs' &&
          (live ? (
            <Logs name={name} services={detail.services.map((s) => s.name)} releaseSeq={live.seq} />
          ) : (
            <p className="text-body text-muted-foreground">Logs appear once a release is live.</p>
          ))}
        {tab === 'settings' && (
          <Settings
            detail={detail}
            onChanged={() => {
              setPending(Boolean(live));
              void reload();
            }}
            onDomainsChanged={() => void reload()}
          />
        )}
      </div>

      <ReleaseSheet
        project={name}
        release={detail.releases.find((r) => r.seq === opened)}
        live={live}
        busy={busy}
        onClose={() => setOpened(null)}
        onRollback={(seq) => void run(`Roll back to r${seq}`, (onLog) => api.rollback(name, seq, onLog), seq)}
      />
    </div>
  );
}

/** One word for the project: live, deploying, degraded (a service is down) or not deployed. */
function ProjectState({ detail, live }: { detail: ProjectDetail; live?: Release }) {
  const newest = detail.releases[0];
  if (newest && ['building', 'releasing', 'starting'].includes(newest.status)) return <StatusBadge tone="warning">Deploying</StatusBadge>;
  if (!live) return <StatusBadge tone="neutral">Not deployed</StatusBadge>;
  if (detail.services.some((s) => s.state !== 'running')) return <StatusBadge tone="warning">Degraded</StatusBadge>;
  return <StatusBadge tone="positive">Live</StatusBadge>;
}

/** The line under the title: the one fact that most needs attention. */
function Finding({ live, failed, pending, newest }: { live?: Release; failed?: Release; pending: boolean; newest?: Release }) {
  const text = 'text-body text-muted-foreground';
  if (failed) {
    return (
      <p className={text}>
        <span className="font-medium text-negative">r{failed.seq} failed</span> <When at={failed.created_at} />
        {failed.error && <>: {failed.error.split('\n')[0].replaceAll('`', '')}</>}.{' '}
        {live ? `r${live.seq} kept serving.` : 'Nothing was live.'}
      </p>
    );
  }
  if (newest && newest !== live && newest.status !== 'superseded' && newest.status !== 'failed') {
    return <p className={text}>r{newest.seq} is deploying. The live release keeps serving until it is healthy.</p>;
  }
  if (!live) return <p className={text}>Nothing is live yet.</p>;
  return (
    <p className={text}>
      <span className="font-mono text-foreground">r{live.seq}</span> live since <When at={live.created_at} />
      {live.commit && (
        <>
          {' '}
          · <span className="font-mono">{shortCommit(live.commit)}</span>
        </>
      )}
      {pending && <span className="text-warning"> · new values apply on redeploy</span>}
    </p>
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
  return { ok: false, unknown: true, error: 'Kayly did not answer in time. Check the releases list.' };
}

function PageSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading project" className="flex flex-col gap-6 p-6">
      <div className="flex flex-col gap-2">
        <Skeleton className="h-7 w-40" />
        <Skeleton className="h-4 w-72" />
      </div>
      <Skeleton className="h-40 w-full rounded-card" />
      <div className="grid gap-6 lg:grid-cols-3">
        <Skeleton className="h-64 rounded-card lg:col-span-2" />
        <Skeleton className="h-64 rounded-card" />
      </div>
    </div>
  );
}
