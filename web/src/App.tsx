import { ChevronsUpDown, LogOut } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import { ApiError, api, type Project } from './api';
import { Palette, modKey } from './palette';
import { ProjectPage, type Tab } from './project';
import { ProjectsPage } from './projects';
import { SignIn } from './signin';
import { Kbd, Lamp } from './ui';

export type Navigate = (path: string) => void;

export const projectPath = (name: string, tab: Tab = 'overview') =>
  '/p/' + encodeURIComponent(name) + (tab === 'overview' ? '' : '/' + tab);

function route(path: string): { project: string; tab: Tab } | null {
  const m = /^\/p\/([^/]+)(?:\/(logs|settings))?\/?$/.exec(path);
  return m ? { project: decodeURIComponent(m[1]), tab: (m[2] as Tab | undefined) ?? 'overview' } : null;
}

export function App() {
  const [path, setPath] = useState(location.pathname);
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [signedOut, setSignedOut] = useState(false);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [palette, setPalette] = useState(false);

  const navigate: Navigate = useCallback((to) => {
    history.pushState(null, '', to);
    setPath(to);
  }, []);

  useEffect(() => {
    const onPop = () => setPath(location.pathname);
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPalette((v) => !v);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const loadProjects = useCallback(async () => {
    try {
      setProjects(await api.projects());
      setSignedOut(false);
      setLoadError(null);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setSignedOut(true);
      else setLoadError(e);
    }
  }, []);

  const current = route(path);
  const project = current?.project;
  useEffect(() => {
    void loadProjects();
  }, [loadProjects, project]);

  useEffect(() => {
    document.title = project ? `${project} · kayly` : 'kayly';
  }, [project]);

  if (signedOut) return <SignIn onSignedIn={loadProjects} />;

  return (
    <div className="flex h-dvh flex-col overflow-hidden">
      <header className="shrink-0 bg-display text-display-ink">
        <nav aria-label="Main" className="flex h-12 items-center gap-2 px-3 sm:px-5">
          <a
            href="/"
            onClick={(e) => {
              e.preventDefault();
              navigate('/');
            }}
            className="flex h-8 items-center gap-2 rounded-[3px] px-2 text-body font-bold tracking-tight text-display-ink no-underline hover:bg-display-hover"
          >
            <Lamp state="live" size={12} />
            kayly
          </a>
          <span aria-hidden="true" className="text-display-rule">
            /
          </span>
          <button
            type="button"
            onClick={() => setPalette(true)}
            aria-haspopup="dialog"
            className="flex h-8 min-w-0 items-center gap-2 rounded-[3px] px-2 text-body text-display-ink transition-colors duration-150 hover:bg-display-hover"
          >
            <span className="truncate font-semibold">{project ?? 'Projects'}</span>
            <ChevronsUpDown size={14} aria-hidden="true" className="shrink-0 text-display-ink-2" />
            <Kbd className="text-display-ink-2 max-sm:hidden">{modKey}K</Kbd>
          </button>
          <button
            type="button"
            onClick={() => void api.signOut().then(() => setSignedOut(true))}
            className="ml-auto inline-flex h-8 items-center gap-2 rounded-[3px] px-2 text-body text-display-ink-2 transition-colors duration-150 hover:bg-display-hover hover:text-display-ink"
          >
            <LogOut size={15} aria-hidden="true" />
            <span className="max-sm:sr-only">Sign out</span>
          </button>
        </nav>
      </header>

      {loadError ? (
        <main className="p-5">
          <p role="alert" className="text-fail-ink">
            Kayly did not answer: {loadError instanceof Error ? loadError.message : String(loadError)}. Reload to try
            again.
          </p>
        </main>
      ) : current ? (
        <ProjectPage key={current.project} name={current.project} tab={current.tab} navigate={navigate} onChanged={loadProjects} />
      ) : (
        <ProjectsPage projects={projects} navigate={navigate} onCreated={loadProjects} />
      )}

      {palette && (
        <Palette
          projects={projects ?? []}
          current={current}
          navigate={navigate}
          onCreated={loadProjects}
          onClose={() => setPalette(false)}
        />
      )}
    </div>
  );
}
