import { ChevronRight, ChevronsUpDown, LayoutGrid, LogOut, Menu, Monitor, Moon, Search, Sun } from 'lucide-react';
import { type MouseEvent, type ReactNode, useState } from 'react';
import { type Navigate, type Route, projectPath } from '@/App';
import type { Project, ServerStatus } from '@/api';
import { Kbd, modKey } from '@/components/common';
import { Mark } from '@/components/mark';
import { StatusDot } from '@/components/status';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet';
import { formatBytes } from '@/lib/format';
import { type Theme, useTheme } from '@/lib/theme';
import { cn } from '@/lib/utils';

type ShellProps = {
  projects: Project[] | null;
  server: ServerStatus | null;
  current: Route;
  navigate: Navigate;
  onSearch: () => void;
  onSignOut: () => void;
  children: ReactNode;
};

/**
 * The frame: a tinted L of sidebar and top bar around one white sheet that holds the page. On a
 * phone the sheet is the screen and the sidebar opens from the menu button.
 */
export function Shell({ projects, server, current, navigate, onSearch, onSignOut, children }: ShellProps) {
  const [menu, setMenu] = useState(false);
  const go = (to: string) => (e: MouseEvent) => {
    e.preventDefault();
    setMenu(false);
    navigate(to);
  };
  const sidebar = <Sidebar projects={projects} server={server} current={current} go={go} onSignOut={onSignOut} />;

  return (
    <div className="flex h-dvh flex-col bg-frame lg:grid lg:grid-cols-[228px_minmax(0,1fr)] lg:grid-rows-[48px_minmax(0,1fr)]">
      <aside aria-label="Sidebar" className="hidden min-h-0 lg:row-span-2 lg:flex">
        {sidebar}
      </aside>

      <header className="grid h-12 shrink-0 grid-cols-[1fr_auto_1fr] items-center gap-3 px-2 lg:pr-2 lg:pl-0">
        <div className="flex min-w-0 items-center gap-1">
          <Button variant="ghost" size="icon" aria-label="Open menu" onClick={() => setMenu(true)} className="lg:hidden">
            <Menu />
          </Button>
          <nav aria-label="Breadcrumb" className="flex min-w-0 items-center gap-1 text-body">
            <a href="/" onClick={go('/')} className={cn('truncate rounded-control px-1.5 py-1 hover:bg-active', current.project ? 'text-muted-foreground' : 'font-medium', 'max-lg:hidden')}>
              Projects
            </a>
            {current.project && (
              <>
                <ChevronRight className="size-3.5 shrink-0 text-muted-foreground max-lg:hidden" aria-hidden="true" />
                <span className="truncate px-1.5 font-medium">{current.project}</span>
              </>
            )}
            {!current.project && <span className="px-1.5 font-medium lg:hidden">Projects</span>}
          </nav>
        </div>
        <button
          type="button"
          onClick={onSearch}
          aria-haspopup="dialog"
          className="flex h-8 w-[min(420px,32vw)] items-center gap-2 rounded-control border bg-background px-2.5 text-body text-muted-foreground transition-colors duration-150 hover:border-input max-md:hidden"
        >
          <Search className="size-4" aria-hidden="true" />
          <span className="flex-1 text-left">Go to a project</span>
          <Kbd>{modKey}K</Kbd>
        </button>
        <div className="flex justify-end">
          <Button variant="ghost" size="icon" aria-label="Go to a project" onClick={onSearch} className="md:hidden">
            <Search />
          </Button>
        </div>
      </header>

      <main
        id="main"
        className="min-h-0 flex-1 overflow-y-auto border-t bg-background lg:mr-2 lg:mb-2 lg:rounded-sheet lg:border"
      >
        {children}
      </main>

      <Sheet open={menu} onOpenChange={setMenu}>
        <SheetContent side="left" className="bg-frame">
          <SheetTitle className="sr-only">Menu</SheetTitle>
          <SheetDescription className="sr-only">Projects and account</SheetDescription>
          {sidebar}
        </SheetContent>
      </Sheet>
    </div>
  );
}

function Sidebar({
  projects,
  server,
  current,
  go,
  onSignOut,
}: {
  projects: Project[] | null;
  server: ServerStatus | null;
  current: Route;
  go: (to: string) => (e: MouseEvent) => void;
  onSignOut: () => void;
}) {
  const row = 'flex h-8 items-center gap-2.5 rounded-control px-2 text-body transition-colors duration-150 hover:bg-active';
  return (
    <div className="flex min-h-0 w-full flex-col">
      <div className="flex h-12 shrink-0 items-center gap-2.5 px-4">
        <Mark />
        <div className="flex min-w-0 flex-col">
          <span className="text-body leading-4 font-semibold">kayly</span>
          <span className="truncate text-small text-muted-foreground" title="This server">
            {location.host}
          </span>
        </div>
      </div>

      <nav aria-label="Projects" className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-2 pt-2">
        <a href="/" onClick={go('/')} aria-current={!current.project ? 'page' : undefined} className={cn(row, !current.project && 'bg-active font-medium')}>
          <LayoutGrid className="size-4 text-muted-foreground" aria-hidden="true" />
          All projects
        </a>
        <p className="mt-5 mb-1 px-2 text-small text-muted-foreground">Projects</p>
        {projects?.map((p) => {
          const active = current.project === p.name;
          return (
            <a
              key={p.name}
              href={projectPath(p.name)}
              onClick={go(projectPath(p.name, active ? current.tab : 'overview'))}
              aria-current={active ? 'page' : undefined}
              className={cn(row, active && 'bg-active font-medium')}
            >
              <StatusDot tone={p.live_release ? 'positive' : 'off'} label={p.live_release ? `r${p.live_release} live` : 'Not deployed'} className="mx-[3px]" />
              <span className="min-w-0 flex-1 truncate">{p.name}</span>
              {p.live_release ? <span className="font-mono text-small text-muted-foreground">r{p.live_release}</span> : null}
            </a>
          );
        })}
      </nav>

      <div className="flex shrink-0 flex-col gap-3 p-2 pb-3">
        {server && server.disk_total > 0 && <DiskLine server={server} />}
        <Account version={server?.version} onSignOut={onSignOut} />
      </div>
    </div>
  );
}

/** The server's disk at its true ratio; amber from 85 %, when Kayly starts sending alerts. */
function DiskLine({ server }: { server: ServerStatus }) {
  const ratio = server.disk_used / server.disk_total;
  const full = ratio >= 0.85;
  return (
    <div className="flex flex-col gap-1.5 px-2">
      <div className="flex items-baseline justify-between text-small">
        <span className="text-muted-foreground">Disk</span>
        <span className={cn('tabular', full ? 'font-medium text-warning' : 'text-muted-foreground')}>
          {formatBytes(server.disk_used)} of {formatBytes(server.disk_total)}
        </span>
      </div>
      <div className="h-1 overflow-hidden rounded-full bg-active" role="meter" aria-label="Disk used" aria-valuenow={Math.round(ratio * 100)} aria-valuemin={0} aria-valuemax={100}>
        <div className={cn('h-full rounded-full', full ? 'bg-warning' : 'bg-muted-foreground')} style={{ width: `${ratio * 100}%` }} />
      </div>
    </div>
  );
}

const themes: { id: Theme; label: string; icon: ReactNode }[] = [
  { id: 'system', label: 'System', icon: <Monitor /> },
  { id: 'light', label: 'Light', icon: <Sun /> },
  { id: 'dark', label: 'Dark', icon: <Moon /> },
];

function Account({ version, onSignOut }: { version?: string; onSignOut: () => void }) {
  const [theme, setTheme] = useTheme();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="flex h-11 items-center gap-2.5 rounded-control px-2 text-left transition-colors duration-150 hover:bg-active data-[state=open]:bg-active">
        <span aria-hidden="true" className="flex size-7 items-center justify-center rounded-full bg-active text-small font-semibold">
          O
        </span>
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="text-body leading-4 font-medium">Owner</span>
          <span className="text-small text-muted-foreground">{version ? `Kayly ${version}` : 'Kayly'}</span>
        </span>
        <ChevronsUpDown className="size-4 text-muted-foreground" aria-hidden="true" />
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" className="w-(--radix-dropdown-menu-trigger-width)">
        <DropdownMenuLabel>Theme</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as Theme)}>
          {themes.map((t) => (
            <DropdownMenuRadioItem key={t.id} value={t.id}>
              {t.icon}
              {t.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onSignOut}>
          <LogOut />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
