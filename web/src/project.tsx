import { ExternalLink } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import { type Navigate, projectPath } from './App';
import { ApiError, api, type ProjectDetail, type Release, type ReleaseStatus, type StreamResult } from './api';
import { Logs } from './logs';
import { Overview } from './overview';
import { Settings } from './settings';
import { ArmGo, ErrorNote, Kbd, Lamp, When } from './ui';

export type Tab = 'overview' | 'logs' | 'settings';

const tabs: { id: Tab; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'logs', label: 'Logs' },
  { id: 'settings', label: 'Settings' },
];

export type Operation = {
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
  const [selected, setSelected] = useState<number | null>(null);
  // Env values and add-ons reach running services only through a new release.
  const [pending, setPending] = useState(false);

  // 1, 2 and 3 switch tabs when the keyboard is not busy with a field.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (e.metaKey || e.ctrlKey || e.altKey || t.closest('input, textarea, select, dialog')) return;
      const next = tabs[Number(e.key) - 1];
      if (next) navigate(projectPath(name, next.id));
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [name, navigate]);

  const run = useCallback(
    async (title: string, start: (onLog: (t: string) => void) => Promise<StreamResult>, rollbackTo?: number) => {
      setOp({ title, log: '' });
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
        setSelected(result.release ?? null);
        setPending(false);
      }
      await reload();
      await onChanged();
    },
    [reload, onChanged, detail, name, navigate],
  );

  if (error && !detail) {
    return (
      <main className="flex flex-col gap-3 p-5">
        <ErrorNote error={error} />
        <a
          href="/"
          onClick={(e) => {
            e.preventDefault();
            navigate('/');
          }}
          className="text-select-ink underline"
        >
          All projects
        </a>
      </main>
    );
  }
  if (!detail) return <PageSkeleton />;

  const live = detail.releases.find((r) => r.status === 'live');
  const shown = detail.releases.find((r) => r.seq === selected) ?? live ?? detail.releases[0];
  const changed = () => {
    setPending(Boolean(live));
    void reload();
  };
  const busy = Boolean(op && !op.result);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <section aria-label="Live release" className="shrink-0 border-b border-display-rule bg-display px-3 pt-3 text-display-ink sm:px-5 sm:pt-4">
        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
          <div className="flex min-w-0 flex-wrap items-center gap-x-5 gap-y-1.5">
            <h1 className="text-title font-bold tracking-tight">{name}</h1>
            {live ? (
              <p className="flex items-center gap-2.5">
                <Lamp state="live" size={16} land={landed === live.seq} />
                <span className="font-mono text-cue font-semibold tracking-tight">r{live.seq}</span>
                <span className="text-meta text-display-ink-2">
                  live since <When at={live.created_at} />
                </span>
              </p>
            ) : (
              <p className="flex items-center gap-2 text-display-ink-2">
                <Lamp state="off" />
                Not deployed yet
              </p>
            )}
            <ServiceLinks detail={detail} />
          </div>
          {live && (
            <div className="flex items-center gap-3 max-sm:w-full max-sm:flex-col max-sm:items-stretch">
              {pending && <span className="text-meta text-display-ink-2">New values apply on redeploy</span>}
              <ArmGo
                label="Redeploy"
                go={`Go: r${live.seq}'s image again`}
                variant={pending ? 'go' : 'display'}
                busy={busy}
                onGo={() => void run('Redeploy', (onLog) => api.redeploy(name, onLog))}
              />
            </div>
          )}
        </div>

        {detail.releases.length > 0 && (
          <StepRow
            releases={detail.releases}
            selected={shown?.seq}
            landed={landed}
            onSelect={(seq) => {
              setSelected(seq);
              navigate(projectPath(name));
            }}
          />
        )}

        <nav aria-label="Project" className="-mx-1 mt-2 flex gap-1">
          {tabs.map((t, i) => (
            <a
              key={t.id}
              href={projectPath(name, t.id)}
              aria-current={t.id === tab ? 'page' : undefined}
              onClick={(e) => {
                e.preventDefault();
                navigate(projectPath(name, t.id));
              }}
              className={`group flex h-10 items-center gap-2 border-b-2 px-2 text-body font-semibold no-underline transition-colors duration-150 ${
                t.id === tab
                  ? 'border-select text-display-ink'
                  : 'border-transparent text-display-ink-2 hover:text-display-ink'
              }`}
            >
              {t.label}
              <Kbd className="text-display-ink-2 opacity-0 transition-opacity duration-150 group-hover:opacity-100 max-sm:hidden">
                {i + 1}
              </Kbd>
            </a>
          ))}
        </nav>
      </section>

      <main className="min-h-0 flex-1 overflow-y-auto p-3 sm:p-5 lg:overflow-hidden">
        {tab === 'overview' && (
          <Overview
            detail={detail}
            shown={shown}
            op={op}
            busy={busy}
            onSelect={setSelected}
            onDismiss={() => setOp(null)}
            onRollback={(seq) => void run(`Roll back to r${seq}`, (onLog) => api.rollback(name, seq, onLog), seq)}
          />
        )}
        {tab === 'logs' &&
          (live ? (
            <Logs name={name} services={detail.services.map((s) => s.name)} releaseSeq={live.seq} />
          ) : (
            <p className="text-ink-2">Logs appear once a release is live.</p>
          ))}
        {tab === 'settings' && <Settings detail={detail} onChanged={changed} onDomainsChanged={() => void reload()} />}
      </main>
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
  return { ok: false, unknown: true, error: 'Kayly did not answer in time. Check the releases list.' };
}

function ServiceLinks({ detail }: { detail: ProjectDetail }) {
  const links = detail.services.filter((s) => s.urls.length > 0);
  if (links.length === 0) return null;
  return (
    <ul className="flex min-w-0 flex-wrap gap-x-4 gap-y-1">
      {links.map((s) => (
        <li key={s.name} className="flex min-w-0 items-center gap-1.5 text-meta">
          {links.length > 1 && <span className="text-display-ink-2">{s.name}</span>}
          <a
            href={s.urls[0]}
            target="_blank"
            rel="noreferrer"
            className="inline-flex min-w-0 items-center gap-1 font-mono text-display-ink underline decoration-display-rule hover:decoration-display-ink"
          >
            <span className="truncate">{s.urls[0].replace(/^https:\/\//, '')}</span>
            <ExternalLink size={12} aria-hidden="true" className="shrink-0 text-display-ink-2" />
          </a>
        </li>
      ))}
    </ul>
  );
}

export const statusWord: Record<ReleaseStatus, string> = {
  live: 'Live',
  superseded: 'Previous',
  failed: 'Failed',
  building: 'Building',
  releasing: 'Release step',
  starting: 'Starting',
};

/** The cell's mark: the same shape language as the lamps, drawn as a step. */
function stepLook(r: Release) {
  switch (r.status) {
    case 'live':
      return 'border-live bg-live';
    case 'failed':
      return 'border-fail bg-fail';
    case 'superseded':
      return r.can_roll_back ? 'border-display-ink-2' : 'border-display-ink-2/30';
    default:
      return 'border-busy light-blink';
  }
}

// How many steps fit: the newest ones; older releases stay in the Overview list.
const stepFit =
  'max-sm:[&:nth-last-child(n+13)]:hidden max-lg:[&:nth-last-child(n+25)]:hidden max-xl:[&:nth-last-child(n+37)]:hidden';

/**
 * The release sequence: one step per release, oldest to newest, the live one lit. Selecting a
 * step opens it in Overview. Arrow keys move along the row.
 */
function StepRow({
  releases,
  selected,
  landed,
  onSelect,
}: {
  releases: Release[];
  selected?: number;
  landed: number | null;
  onSelect: (seq: number) => void;
}) {
  const steps = [...releases].sort((a, b) => a.seq - b.seq).slice(-48);
  return (
    <ol
      aria-label="Releases, oldest to newest"
      className="mt-4 flex gap-1"
      onKeyDown={(e) => {
        const i = steps.findIndex((r) => r.seq === selected);
        const d = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
        const next = steps[i + d];
        if (!d || !next) return;
        e.preventDefault();
        onSelect(next.seq);
        (e.currentTarget.querySelector(`[data-seq="${next.seq}"]`) as HTMLElement | null)?.focus();
      }}
    >
      {steps.map((r) => {
        const isSelected = r.seq === selected;
        return (
          <li key={r.seq} className={`flex w-6 flex-col items-center gap-1 ${stepFit}`}>
            <button
              type="button"
              data-seq={r.seq}
              tabIndex={isSelected ? 0 : -1}
              aria-current={isSelected || undefined}
              aria-label={`r${r.seq}, ${statusWord[r.status]}`}
              title={`r${r.seq} · ${statusWord[r.status]}`}
              onClick={() => onSelect(r.seq)}
              className={`relative size-6 rounded-[3px] border-2 transition-[outline-color] duration-150 hover:outline hover:outline-1 hover:outline-offset-2 hover:outline-display-ink-2 ${stepLook(r)} ${
                isSelected ? 'outline outline-2 outline-offset-2 !outline-select' : ''
              } ${r.status === 'live' && landed === r.seq ? 'light-land' : ''}`}
            >
              {r.status === 'failed' && (
                <svg viewBox="0 0 20 20" aria-hidden="true" className="absolute inset-0 size-full">
                  <path d="M5 15 15 5" stroke="var(--k-display)" strokeWidth="2.5" strokeLinecap="round" />
                </svg>
              )}
            </button>
            <span
              aria-hidden="true"
              className={`font-mono text-meta ${isSelected || r.status === 'live' ? 'text-display-ink' : 'text-display-ink-2'}`}
            >
              {r.seq}
            </span>
          </li>
        );
      })}
    </ol>
  );
}

function PageSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading project" className="flex min-h-0 flex-1 flex-col">
      <div className="h-40 shrink-0 bg-display" />
      <div className="grid flex-1 gap-5 p-5 lg:grid-cols-[minmax(0,1fr)_380px]">
        <div className="rounded-md border border-rule bg-surface" />
        <div className="rounded-md border border-rule bg-surface max-lg:hidden" />
      </div>
    </div>
  );
}
