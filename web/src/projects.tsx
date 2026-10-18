import { type SubmitEvent, useEffect, useState } from 'react';
import { type Navigate, projectPath } from './App';
import { api, type Project, type ServerStatus } from './api';
import { modKey } from './palette';
import { Button, Command, formatBytes, Kbd, Lamp, Pane, When } from './ui';

export function ProjectsPage({
  projects,
  navigate,
  onCreated,
}: {
  projects: Project[] | null;
  navigate: Navigate;
  onCreated: () => Promise<void>;
}) {
  const live = projects?.filter((p) => p.live_release).length ?? 0;
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <section className="shrink-0 border-b border-display-rule bg-display px-3 pt-3 pb-5 text-display-ink sm:px-5 sm:pt-4">
        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
          <h1 className="flex items-baseline gap-3">
            <span className="text-title font-bold tracking-tight">Projects</span>
            {projects && projects.length > 0 && (
              <span className="text-meta text-display-ink-2">
                {live} of {projects.length} live
              </span>
            )}
          </h1>
          <CreateProject navigate={navigate} onCreated={onCreated} />
        </div>
        <ServerLine />
      </section>
      <main className="min-h-0 flex-1 p-3 sm:p-5">
        {projects === null ? (
          <div aria-busy="true" aria-label="Loading projects" className="h-40 rounded-md border border-rule bg-surface" />
        ) : projects.length === 0 ? (
          <FirstRun />
        ) : (
          <Pane
            id="all-projects"
            title="All projects"
            action={
              <span className="flex items-center gap-1.5 text-meta text-ink-2 max-sm:hidden">
                <Kbd>{modKey}K</Kbd> to jump
              </span>
            }
            className="max-h-full"
          >
            <ul>
              {projects.map((p) => (
                <li key={p.name} className="border-b border-rule last:border-b-0">
                  <a
                    href={projectPath(p.name)}
                    onClick={(e) => {
                      e.preventDefault();
                      navigate(projectPath(p.name));
                    }}
                    className="grid min-h-12 grid-cols-[12px_minmax(0,1fr)_auto] items-center gap-x-4 px-4 text-ink no-underline transition-colors duration-150 hover:bg-hover sm:grid-cols-[12px_minmax(0,1fr)_8rem_10rem]"
                  >
                    <Lamp state={p.live_release ? 'live' : 'off'} />
                    <span className="truncate font-semibold">{p.name}</span>
                    <span className={`font-mono text-meta ${p.live_release ? '' : 'text-ink-2'}`}>
                      {p.live_release ? `r${p.live_release}` : 'not deployed'}
                    </span>
                    <span className="text-right text-meta text-ink-2 max-sm:hidden">
                      {p.live_since ? <When at={p.live_since} /> : <>created <When at={p.created_at} /></>}
                    </span>
                  </a>
                </li>
              ))}
            </ul>
          </Pane>
        )}
      </main>
    </div>
  );
}

function CreateProject({ navigate, onCreated }: { navigate: Navigate; onCreated: () => Promise<void> }) {
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const p = await api.createProject(name.trim());
      await onCreated();
      navigate(projectPath(p.name));
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-1.5 max-sm:w-full">
      <div className="flex gap-2">
        <label className="min-w-0 flex-1">
          <span className="sr-only">New project name</span>
          <input
            value={name}
            onChange={(e) => setName(e.target.value.toLowerCase())}
            placeholder="new-project"
            pattern="[a-z][a-z0-9-]{0,30}"
            title="Lowercase letters, digits and dashes, starting with a letter"
            required
            aria-invalid={error ? true : undefined}
            className="h-9 w-56 rounded-[3px] border border-display-rule bg-display px-3 font-mono text-body text-display-ink transition-colors duration-150 placeholder:text-display-ink-2 hover:border-display-ink-2 focus:border-select focus:outline-none aria-[invalid=true]:border-fail max-sm:w-full"
          />
        </label>
        <Button type="submit" variant="go" busy={busy}>
          Create project
        </Button>
      </div>
      {error ? (
        <p role="alert" className="text-meta font-semibold text-fail">
          {error instanceof Error ? error.message : String(error)}
        </p>
      ) : null}
    </form>
  );
}

function FirstRun() {
  return (
    <div className="grid gap-3 sm:gap-5 md:grid-cols-2">
      <Pane id="first-setup" title="Set it up here first">
        <p className="max-w-[60ch] p-4 text-ink-2">
          Create a project above, attach Postgres or Redis, and set the values your app needs. Its first deploy then
          starts with everything in place.
        </p>
      </Pane>
      <Pane id="first-deploy" title="Or deploy straight away">
        <div className="flex flex-col gap-3 p-4">
          <p className="max-w-[60ch] text-ink-2">
            From the app's folder, next to its <code className="font-mono text-ink">kayly.toml</code>. Kayly lists any
            value it still needs.
          </p>
          <Command>kayly deploy</Command>
        </div>
      </Pane>
    </div>
  );
}

/** The server's disk and memory under the page title; amber from 85 % of the disk. */
function ServerLine() {
  const [s, setS] = useState<ServerStatus | null>(null);
  useEffect(() => {
    let stop = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      try {
        const next = await api.server();
        if (!stop) setS(next);
      } catch {
        // the page works without it
      }
      if (!stop) timer = setTimeout(tick, 60_000);
    };
    void tick();
    return () => {
      stop = true;
      clearTimeout(timer);
    };
  }, []);
  if (!s || s.disk_total === 0) return null;
  const pct = Math.round((s.disk_used / s.disk_total) * 100);
  return (
    <p className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-meta text-display-ink-2">
      <span className={pct >= 85 ? 'font-semibold text-busy' : ''}>
        Disk {formatBytes(s.disk_used)} of {formatBytes(s.disk_total)} ({pct}%)
      </span>
      {s.memory_total > 0 && (
        <span>
          Memory {formatBytes(s.memory_total - s.memory_available)} of {formatBytes(s.memory_total)}
        </span>
      )}
      <span>Kayly {s.version}</span>
    </p>
  );
}
