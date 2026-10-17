import { type SubmitEvent, useState } from 'react';
import type { Navigate } from './App';
import { api, type Project } from './api';
import { Button, Command, ErrorNote, Lamp, SpikeStrip, TextInput, When } from './ui';

export function ProjectsPage({
  projects,
  navigate,
  onCreated,
}: {
  projects: Project[] | null;
  navigate: Navigate;
  onCreated: () => Promise<void>;
}) {
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <h1 className="text-title font-bold">Projects</h1>
        <CreateProject navigate={navigate} onCreated={onCreated} />
      </div>
      {projects === null ? (
        <ListSkeleton />
      ) : projects.length === 0 ? (
        <FirstRun />
      ) : (
        <div className="overflow-hidden rounded-lg border border-rule bg-sheet">
          <table className="w-full border-collapse text-left">
            <thead className="sr-only">
              <tr>
                <th>Project</th>
                <th>Live release</th>
                <th>Since</th>
              </tr>
            </thead>
            <tbody>
              {projects.map((p) => (
                <tr key={p.name} className="group border-b border-rule last:border-b-0 hover:bg-hover">
                  <td className="p-0">
                    <a
                      href={'/p/' + encodeURIComponent(p.name)}
                      onClick={(e) => {
                        e.preventDefault();
                        navigate('/p/' + encodeURIComponent(p.name));
                      }}
                      className="flex h-12 items-center gap-3 px-4 font-semibold text-ink no-underline group-hover:underline"
                    >
                      <SpikeStrip name={p.name} />
                      {p.name}
                    </a>
                  </td>
                  <td className="px-4">
                    {p.live_release ? (
                      <span className="flex items-center gap-2">
                        <Lamp state="live" />
                        <span className="font-mono">r{p.live_release}</span>
                      </span>
                    ) : (
                      <span className="flex items-center gap-2 text-ink-2">
                        <Lamp state="off" />
                        Not deployed
                      </span>
                    )}
                  </td>
                  <td className="px-4 text-right text-meta text-ink-2 max-sm:hidden">
                    {p.live_since ? <When at={p.live_since} /> : <>created <When at={p.created_at} /></>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
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
      navigate('/p/' + encodeURIComponent(p.name));
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
          <TextInput
            value={name}
            onChange={(e) => setName(e.target.value.toLowerCase())}
            placeholder="new-project"
            pattern="[a-z][a-z0-9-]{0,30}"
            title="Lowercase letters, digits and dashes, starting with a letter"
            required
            aria-invalid={error ? true : undefined}
            className="w-56 font-mono max-sm:w-full"
          />
        </label>
        <Button type="submit" variant="primary" busy={busy}>
          Create project
        </Button>
      </div>
      <ErrorNote error={error} />
    </form>
  );
}

function FirstRun() {
  return (
    <div className="grid gap-4 rounded-lg border border-rule bg-sheet p-5 md:grid-cols-2 md:gap-8">
      <div className="flex flex-col gap-2">
        <h2 className="text-body font-bold">Set it up here first</h2>
        <p className="max-w-[60ch] text-ink-2">
          Create a project, attach Postgres or Redis, and set the values your app needs. Its first deploy then starts
          with everything in place.
        </p>
      </div>
      <div className="flex flex-col gap-2">
        <h2 className="text-body font-bold">Or deploy straight away</h2>
        <p className="max-w-[60ch] text-ink-2">
          From the app's folder, next to its <code className="font-mono">kayly.toml</code>. Kayly lists any value it
          still needs.
        </p>
        <Command>kayly deploy</Command>
      </div>
    </div>
  );
}

function ListSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading projects" className="overflow-hidden rounded-lg border border-rule bg-sheet">
      {[0, 1, 2].map((i) => (
        <div key={i} className="flex h-12 items-center gap-3 border-b border-rule px-4 last:border-b-0">
          <span className="h-[7px] w-[22px] rounded-[1px] bg-rule" />
          <span className="h-3 w-32 rounded bg-rule motion-safe:animate-pulse" />
        </div>
      ))}
    </div>
  );
}
