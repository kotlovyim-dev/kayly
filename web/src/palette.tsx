import { FileText, LayoutGrid, Plus, ScrollText, Settings2 } from 'lucide-react';
import { useState } from 'react';
import { type Navigate, type Route, projectPath } from '@/App';
import { api, type Project } from '@/api';
import { ErrorNote } from '@/components/common';
import { StatusDot } from '@/components/status';
import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command';

const validName = /^[a-z][a-z0-9-]{0,30}$/;

/** ⌘K: jump to a project or a tab, or create a project, from the keyboard. */
export function Palette({
  open,
  onOpenChange,
  projects,
  current,
  navigate,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  projects: Project[];
  current: Route;
  navigate: Navigate;
  onCreated: () => Promise<void>;
}) {
  const [query, setQuery] = useState('');
  const [error, setError] = useState<unknown>(null);
  const q = query.trim().toLowerCase();
  const go = (to: string) => {
    navigate(to);
    onOpenChange(false);
    setQuery('');
  };
  const tab = current.project ? current.tab : 'overview';

  return (
    <CommandDialog
      open={open}
      onOpenChange={(o) => {
        onOpenChange(o);
        if (!o) setError(null);
      }}
      title="Go to"
      description="Jump to a project or a tab, or type a new project name"
    >
      <CommandInput value={query} onValueChange={setQuery} placeholder="Jump to a project, or type a new name" />
      <CommandList>
        <CommandEmpty>Nothing matches. A project name uses lowercase letters, digits and dashes.</CommandEmpty>
        <CommandGroup heading="Projects">
          <CommandItem value="all projects" onSelect={() => go('/')}>
            <LayoutGrid />
            All projects
          </CommandItem>
          {projects.map((p) => (
            <CommandItem key={p.name} value={p.name} onSelect={() => go(projectPath(p.name, tab))}>
              <StatusDot tone={p.live_release ? 'positive' : 'off'} className="mx-[3px]" />
              <span className="flex-1 truncate">{p.name}</span>
              <span className="font-mono text-small text-muted-foreground">{p.live_release ? `r${p.live_release}` : 'not deployed'}</span>
            </CommandItem>
          ))}
        </CommandGroup>
        {current.project && (
          <CommandGroup heading={current.project}>
            <CommandItem value="overview" onSelect={() => go(projectPath(current.project!, 'overview'))}>
              <FileText />
              Overview
            </CommandItem>
            <CommandItem value="logs" onSelect={() => go(projectPath(current.project!, 'logs'))}>
              <ScrollText />
              Logs
            </CommandItem>
            <CommandItem value="settings" onSelect={() => go(projectPath(current.project!, 'settings'))}>
              <Settings2 />
              Settings
            </CommandItem>
          </CommandGroup>
        )}
        {validName.test(q) && !projects.some((p) => p.name === q) && (
          <CommandGroup heading="New">
            <CommandItem
              value={`create ${q}`}
              onSelect={async () => {
                try {
                  const p = await api.createProject(q);
                  await onCreated();
                  go(projectPath(p.name));
                } catch (e) {
                  setError(e);
                }
              }}
            >
              <Plus />
              Create project <span className="font-mono">{q}</span>
            </CommandItem>
          </CommandGroup>
        )}
      </CommandList>
      {error ? <ErrorNote error={error} className="border-t px-4 py-3" /> : null}
    </CommandDialog>
  );
}
