import { Fragment, useEffect, useRef, useState } from 'react';
import { api, type ProjectDetail, type Release, type ReleaseStatus, type Service } from './api';
import { type Operation, statusWord } from './project';
import { ArmGo, Button, Command, ErrorNote, formatBytes, Lamp, type LampState, Pane, When } from './ui';

function lampFor(s: ReleaseStatus): LampState {
  if (s === 'live' || s === 'failed' || s === 'superseded') return s;
  return 'standby';
}

/** Overview: the release history beside the selected release (or the running operation). */
export function Overview({
  detail,
  shown,
  op,
  busy,
  onSelect,
  onDismiss,
  onRollback,
}: {
  detail: ProjectDetail;
  shown?: Release;
  op: Operation | null;
  busy: boolean;
  onSelect: (seq: number) => void;
  onDismiss: () => void;
  onRollback: (seq: number) => void;
}) {
  if (detail.releases.length === 0 && !op) {
    return (
      <Pane id="first-deploy" title="First deploy" className="max-w-2xl">
        <div className="flex flex-col gap-3 p-4">
          <p className="max-w-[62ch] text-ink-2">
            Deploy from the app's folder, next to its <code className="font-mono text-ink">kayly.toml</code>. Add-ons
            and values set in Settings are ready for it.
          </p>
          <Command>{`kayly deploy -p ${detail.name}`}</Command>
        </div>
      </Pane>
    );
  }
  return (
    <div className="grid gap-3 sm:gap-5 lg:h-full lg:grid-cols-[minmax(0,1fr)_minmax(340px,420px)]">
      <div className="flex min-h-0 flex-col gap-3 sm:gap-5 lg:order-2">
        {op ? (
          <OperationPane op={op} onDismiss={onDismiss} />
        ) : (
          shown && <ReleaseDetail project={detail.name} release={shown} busy={busy} onRollback={onRollback} />
        )}
        <Services detail={detail} />
      </div>
      <Pane id="releases" title="Releases" className="lg:order-1">
        <ul>
          {detail.releases.map((r) => (
            <li key={r.seq} className="border-b border-rule last:border-b-0">
              <button
                type="button"
                aria-current={r.seq === shown?.seq || undefined}
                onClick={() => onSelect(r.seq)}
                className={`grid min-h-11 w-full grid-cols-[12px_3.5rem_minmax(0,1fr)_auto] items-center gap-3 px-4 py-2 text-left transition-colors duration-150 ${
                  r.seq === shown?.seq ? 'bg-select/10' : 'hover:bg-hover'
                }`}
              >
                <Lamp state={lampFor(r.status)} />
                <span className={`font-mono font-semibold ${r.seq === shown?.seq ? 'text-select-ink' : ''}`}>
                  r{r.seq}
                </span>
                <span className="flex min-w-0 items-baseline gap-3">
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
                  {r.error ? (
                    <span className="truncate text-meta text-ink-2">{r.error.split('\n')[0].replaceAll('`', '')}</span>
                  ) : (
                    r.commit && (
                      <span className="truncate text-meta text-ink-2" title={r.commit_message}>
                        <code className="font-mono">{shortCommit(r.commit)}</code> {r.commit_message}
                      </span>
                    )
                  )}
                </span>
                <span className="text-meta whitespace-nowrap text-ink-2">
                  <When at={r.created_at} />
                </span>
              </button>
            </li>
          ))}
        </ul>
      </Pane>
    </div>
  );
}

/** "3a77de6" for a commit, with a star when the work tree had uncommitted changes. */
function shortCommit(commit: string) {
  const dirty = commit.endsWith('-dirty');
  return commit.slice(0, 7) + (dirty ? '*' : '');
}

function ReleaseDetail({
  project,
  release: r,
  busy,
  onRollback,
}: {
  project: string;
  release: Release;
  busy: boolean;
  onRollback: (seq: number) => void;
}) {
  return (
    <Pane
      id="release"
      title={
        <span className="flex items-center gap-2.5">
          <Lamp state={lampFor(r.status)} />
          <span className="font-mono">r{r.seq}</span>
          <span className="font-normal text-ink-2">{statusWord[r.status]}</span>
        </span>
      }
      className="shrink-0"
    >
      <div className="flex flex-col gap-3 p-4">
        <p className="text-meta text-ink-2">
          Created <When at={r.created_at} /> · {new Date(r.created_at).toLocaleString()}
        </p>
        {r.commit && (
          <p className="text-meta text-ink-2">
            Built from <code className="font-mono text-ink">{shortCommit(r.commit)}</code>
            {r.commit.endsWith('-dirty') && ' with uncommitted changes'}
            {r.commit_message && <> · {r.commit_message}</>}
          </p>
        )}
        {r.error && <FailureReason error={r.error} />}
        {r.status === 'live' && <p className="text-ink-2">Serving now. Redeploy runs this image again.</p>}
        {r.status === 'superseded' &&
          (r.can_roll_back ? (
            <ArmGo
              label={`Roll back to r${r.seq}`}
              go={`Go: r${r.seq} live`}
              busy={busy}
              onGo={() => !busy && onRollback(r.seq)}
              className="self-start"
            />
          ) : (
            <p className="text-ink-2">Its image was pruned, so it cannot be rolled back to.</p>
          ))}
        {r.status === 'failed' && <p className="text-ink-2">The live release kept serving.</p>}
        <DeployLog key={r.seq} project={project} seq={r.seq} />
      </div>
    </Pane>
  );
}

/** The output of the deploy that made a release, loaded when asked for. */
function DeployLog({ project, seq }: { project: string; seq: number }) {
  const [log, setLog] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  if (log !== null) {
    return (
      <pre
        tabIndex={0}
        aria-label={`Deploy log of r${seq}`}
        className="max-h-72 overflow-auto rounded-[3px] bg-code p-3 font-mono text-meta whitespace-pre-wrap"
      >
        {log.replace(/^\n+/, '') || 'The deploy wrote nothing.'}
      </pre>
    );
  }
  return (
    <div className="flex flex-col gap-2">
      <Button
        small
        busy={busy}
        className="self-start"
        onClick={async () => {
          setBusy(true);
          setError(null);
          try {
            setLog(await api.releaseLog(project, seq));
          } catch (e) {
            setError(e);
          } finally {
            setBusy(false);
          }
        }}
      >
        Show deploy log
      </Button>
      {error ? <ErrorNote error={error} /> : null}
    </div>
  );
}

function FailureReason({ error }: { error: string }) {
  const [first, ...rest] = error.split('\n');
  return (
    <div className="flex flex-col gap-2">
      <p className="font-semibold text-fail-ink">
        <Inline text={first} />
      </p>
      {rest.length > 0 && (
        <pre className="max-h-48 overflow-auto rounded-[3px] bg-code p-3 font-mono text-meta whitespace-pre-wrap">
          {rest.join('\n')}
        </pre>
      )}
    </div>
  );
}

/** Renders `quoted` spans of Kayly's messages as commands that never wrap mid-token. */
function Inline({ text }: { text: string }) {
  return (
    <>
      {text.split('`').map((part, i) =>
        i % 2 === 1 ? (
          <code key={i} className="font-mono font-normal text-ink">
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
function OperationPane({ op, onDismiss }: { op: Operation; onDismiss: () => void }) {
  const ref = useRef<HTMLPreElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [op.log]);
  const state: LampState = !op.result ? 'standby' : op.result.ok ? 'live' : 'failed';
  return (
    <section aria-labelledby="operation" className="flex min-h-64 flex-1 flex-col rounded-md border border-rule bg-surface">
      <header className="flex min-h-11 shrink-0 items-center gap-3 border-b border-rule px-4">
        <Lamp state={state} />
        <h2 id="operation" className="min-w-0 flex-1 font-semibold" aria-live="polite">
          {!op.result
            ? `${op.title}: in progress`
            : op.result.ok
              ? `${op.title}: r${op.result.release} is live`
              : op.result.unknown
                ? `${op.title}: result unknown`
                : `${op.title} failed. The live release kept serving.`}
        </h2>
        {op.result && (
          <Button small onClick={onDismiss}>
            Close
          </Button>
        )}
      </header>
      {op.result && !op.result.ok && op.result.error && (
        <p className="border-b border-rule px-4 py-2 text-meta font-semibold text-fail-ink">{op.result.error}</p>
      )}
      <pre
        ref={ref}
        tabIndex={0}
        aria-label={`${op.title} output`}
        className="min-h-0 flex-1 overflow-auto rounded-b-md bg-code px-4 py-3 font-mono text-meta whitespace-pre-wrap"
      >
        {op.log.replace(/^\n+/, '') || 'Waiting for the first line…'}
      </pre>
    </section>
  );
}

const POLL_MS = 10_000;

/** The live release's services, refreshed every few seconds with CPU and memory. */
function useServices(detail: ProjectDetail): Service[] {
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
        // keep showing the last answer; the project view already reports a broken API
      }
      if (!stop) timer = setTimeout(tick, POLL_MS);
    };
    void tick();
    return () => {
      stop = true;
      clearTimeout(timer);
    };
  }, [name, detail.live_release]);
  return fresh ?? detail.services;
}

const serviceLamp: Record<Service['state'], LampState> = {
  running: 'live',
  restarting: 'standby',
  exited: 'failed',
  missing: 'failed',
};

function Services({ detail }: { detail: ProjectDetail }) {
  const services = useServices(detail);
  if (services.length === 0) return null;
  return (
    <Pane id="services" title="Services" className="max-lg:shrink-0 lg:flex-1">
      <ul>
        {services.map((s) => (
          <li key={s.name} className="flex min-h-11 flex-col justify-center gap-0.5 border-b border-rule px-4 py-2 last:border-b-0">
            <span className="flex items-baseline gap-2">
              <Lamp state={serviceLamp[s.state] ?? 'off'} size={10} />
              <span className="font-semibold">{s.name}</span>
              <span className="text-meta text-ink-2">{s.port ? `port ${s.port}` : 'no port'}</span>
              {s.state !== 'running' && <span className="text-meta font-semibold text-fail-ink">{s.state}</span>}
              {s.restarts > 0 && (
                <span className="text-meta text-ink-2">
                  {s.restarts} restart{s.restarts === 1 ? '' : 's'}
                </span>
              )}
            </span>
            {s.stats && (
              <span className="font-mono text-meta text-ink-2">
                {s.stats.cpu_percent.toFixed(1)}% CPU · {formatBytes(s.stats.memory_bytes)}
                {s.memory_limit ? <> of {formatBytes(s.memory_limit)}</> : null}
              </span>
            )}
            {s.urls[0] ? (
              <a
                href={s.urls[0]}
                target="_blank"
                rel="noreferrer"
                className="truncate font-mono text-meta text-ink underline decoration-rule hover:decoration-ink"
              >
                {s.urls[0].replace(/^https:\/\//, '')}
              </a>
            ) : (
              s.command && <code className="truncate font-mono text-meta text-ink-2">{s.command}</code>
            )}
          </li>
        ))}
      </ul>
    </Pane>
  );
}
