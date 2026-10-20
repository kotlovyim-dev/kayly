import { Plus } from 'lucide-react';
import { type FormEvent, type MouseEvent, type ReactNode, useState } from 'react';
import { type Navigate, projectPath } from '@/App';
import { api, type Project, type ServerStatus } from '@/api';
import { CommandLine } from '@/components/copy';
import { ErrorNote, When } from '@/components/common';
import { StatusBadge, StatusDot } from '@/components/status';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { formatBytes } from '@/lib/format';
import { cn } from '@/lib/utils';

export function ProjectsPage({
  projects,
  server,
  navigate,
  onCreated,
}: {
  projects: Project[] | null;
  server: ServerStatus | null;
  navigate: Navigate;
  onCreated: () => Promise<void>;
}) {
  const live = projects?.filter((p) => p.live_release).length ?? 0;
  return (
    <div className="flex flex-col gap-6 p-4 sm:p-6">
      <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="flex flex-col gap-1">
          <h1 className="text-title font-semibold">Projects</h1>
          {projects && projects.length > 0 && (
            <p className="text-body text-muted-foreground">
              {live} of {projects.length} live{live < projects.length && ' · the rest wait for their first deploy'}
            </p>
          )}
        </div>
        <CreateProject navigate={navigate} onCreated={onCreated} />
      </div>

      <ServerStrip server={server} projects={projects} />

      {projects === null ? (
        <div aria-busy="true" aria-label="Loading projects" className="rounded-card border">
          {[0, 1, 2].map((i) => (
            <div key={i} className="flex h-12 items-center gap-4 border-b border-rule px-4 last:border-b-0">
              <Skeleton className="h-4 w-40" />
              <Skeleton className="ml-auto h-4 w-16" />
            </div>
          ))}
        </div>
      ) : projects.length === 0 ? (
        <FirstRun />
      ) : (
        <ProjectTable projects={projects} navigate={navigate} />
      )}
    </div>
  );
}

function ProjectTable({ projects, navigate }: { projects: Project[]; navigate: Navigate }) {
  const open = (name: string) => (e: MouseEvent) => {
    e.preventDefault();
    navigate(projectPath(name));
  };
  return (
    <section aria-labelledby="all-projects" className="rounded-card border">
      <h2 id="all-projects" className="sr-only">
        All projects
      </h2>
      <table className="w-full text-body max-sm:hidden">
        <thead>
          <tr className="h-10 text-left text-small text-muted-foreground">
            <th className="pl-4 font-normal">Project</th>
            <th className="font-normal">Status</th>
            <th className="font-normal">Live release</th>
            <th className="pr-4 text-right font-normal">Live since</th>
          </tr>
        </thead>
        <tbody>
          {projects.map((p) => (
            <tr key={p.name} onClick={open(p.name)} className="h-12 cursor-pointer border-t border-rule transition-colors duration-150 hover:bg-raised">
              <td className="pl-4">
                <a href={projectPath(p.name)} onClick={open(p.name)} className="font-medium hover:underline">
                  {p.name}
                </a>
              </td>
              <td>{p.live_release ? <StatusBadge tone="positive">Live</StatusBadge> : <StatusBadge tone="neutral">Not deployed</StatusBadge>}</td>
              <td className="font-mono">{p.live_release ? `r${p.live_release}` : <span className="text-muted-foreground">—</span>}</td>
              <td className="pr-4 text-right text-muted-foreground">
                {p.live_since ? <When at={p.live_since} /> : <>created <When at={p.created_at} /></>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <ul className="sm:hidden">
        {projects.map((p) => (
          <li key={p.name} className="border-t border-rule first:border-t-0">
            <a href={projectPath(p.name)} onClick={open(p.name)} className="flex min-h-14 items-center gap-3 px-4 py-2">
              <StatusDot tone={p.live_release ? 'positive' : 'off'} />
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="truncate font-medium">{p.name}</span>
                <span className="text-small text-muted-foreground">
                  {p.live_since ? <>live since <When at={p.live_since} /></> : 'not deployed'}
                </span>
              </span>
              <span className="font-mono text-body">{p.live_release ? `r${p.live_release}` : ''}</span>
            </a>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** The server in four divided cells: what fills up, and what runs it. */
function ServerStrip({ server, projects }: { server: ServerStatus | null; projects: Project[] | null }) {
  const diskRatio = server && server.disk_total > 0 ? server.disk_used / server.disk_total : 0;
  const cells: { label: string; value: ReactNode; sub: ReactNode; warn?: boolean }[] = server
    ? [
        {
          label: 'Disk used',
          value: `${Math.round(diskRatio * 100)}%`,
          sub: `${formatBytes(server.disk_used)} of ${formatBytes(server.disk_total)}`,
          warn: diskRatio >= 0.85,
        },
        {
          label: 'Memory used',
          value: server.memory_total ? `${Math.round(((server.memory_total - server.memory_available) / server.memory_total) * 100)}%` : '—',
          sub: server.memory_total ? `${formatBytes(server.memory_total - server.memory_available)} of ${formatBytes(server.memory_total)}` : '',
        },
        { label: 'Projects live', value: projects ? `${projects.filter((p) => p.live_release).length} of ${projects.length}` : '—', sub: 'serving traffic' },
        { label: 'Kayly', value: server.version, sub: server.docker_version ? `Docker ${server.docker_version}` : '' },
      ]
    : [];
  return (
    <section aria-label="Server" className="grid grid-cols-2 rounded-card border md:grid-cols-4">
      {server
        ? cells.map((c, i) => (
            <div key={c.label} className={cn('flex flex-col gap-1 p-4', i % 2 === 1 && 'border-l', i >= 2 && 'max-md:border-t', i === 2 && 'md:border-l')}>
              <span className="text-small text-muted-foreground">{c.label}</span>
              <span className={cn('tabular text-figure', c.warn && 'text-warning')}>{c.value}</span>
              <span className="tabular text-small text-muted-foreground">{c.sub}</span>
            </div>
          ))
        : [0, 1, 2, 3].map((i) => (
            <div key={i} className={cn('flex flex-col gap-2 p-4', i > 0 && 'md:border-l', i === 2 && 'max-md:border-l-0', i % 2 === 1 && 'border-l')}>
              <Skeleton className="h-3 w-16" />
              <Skeleton className="h-7 w-20" />
              <Skeleton className="h-3 w-24" />
            </div>
          ))}
    </section>
  );
}

function CreateProject({ navigate, onCreated }: { navigate: Navigate; onCreated: () => Promise<void> }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const p = await api.createProject(name.trim());
      await onCreated();
      setOpen(false);
      setName('');
      navigate(projectPath(p.name, 'settings'));
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button>
          <Plus />
          New project
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-5">
          <DialogHeader>
            <DialogTitle>New project</DialogTitle>
            <DialogDescription>Set up its add-ons and env values before the first deploy, or deploy straight away.</DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-2">
            <Label htmlFor="new-project">Name</Label>
            <Input
              id="new-project"
              value={name}
              onChange={(e) => setName(e.target.value.toLowerCase())}
              placeholder="my-app"
              pattern="[a-z][a-z0-9-]{0,30}"
              title="Lowercase letters, digits and dashes, starting with a letter"
              required
              autoFocus
              aria-invalid={error ? true : undefined}
              className="font-mono"
            />
            <span className="text-small text-muted-foreground">
              Services are reached at <span className="font-mono">&lt;service&gt;.{name || 'my-app'}.&lt;ip&gt;.sslip.io</span>
            </span>
          </div>
          <ErrorNote error={error} />
          <DialogFooter>
            <Button type="submit" busy={busy}>
              Create project
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function FirstRun() {
  return (
    <section aria-labelledby="first-run" className="grid gap-6 rounded-card border p-5 md:grid-cols-2">
      <div className="flex flex-col gap-2">
        <h2 id="first-run" className="text-section font-semibold">
          Deploy your first app
        </h2>
        <p className="max-w-[60ch] text-body text-muted-foreground">
          Describe its services in <code className="font-mono text-foreground">kayly.toml</code> at the root of the app, then deploy
          from that folder. Kayly creates the project and lists any value it still needs.
        </p>
      </div>
      <div className="flex flex-col justify-center gap-2">
        <CommandLine>kayly deploy</CommandLine>
        <p className="text-small text-muted-foreground">Or create the project here first to attach Postgres or Redis before it runs.</p>
      </div>
    </section>
  );
}
