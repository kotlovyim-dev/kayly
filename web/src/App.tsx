import { useCallback, useEffect, useState } from 'react';
import { ApiError, api, type Project, type ServerStatus } from '@/api';
import { Boundary } from '@/components/boundary';
import { Palette } from '@/palette';
import { ProjectPage, type Tab } from '@/project';
import { ProjectsPage } from '@/projects';
import { Shell } from '@/shell';
import { SignIn } from '@/signin';

export type Navigate = (path: string) => void;
export type Route = { project?: string; tab: Tab };

export const projectPath = (name: string, tab: Tab = 'overview') =>
  '/p/' + encodeURIComponent(name) + (tab === 'overview' ? '' : '/' + tab);

export function route(path: string): Route {
  const m = /^\/p\/([^/]+)(?:\/(logs|settings))?\/?$/.exec(path);
  return m ? { project: decodeURIComponent(m[1]), tab: (m[2] as Tab | undefined) ?? 'overview' } : { tab: 'overview' };
}

/** The server's disk, memory and versions, refreshed every minute. */
function useServer(enabled: boolean) {
  const [server, setServer] = useState<ServerStatus | null>(null);
  useEffect(() => {
    if (!enabled) return;
    let stop = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      try {
        const s = await api.server();
        if (!stop) setServer(s);
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
  }, [enabled]);
  return server;
}

export function App() {
  const [path, setPath] = useState(location.pathname);
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [signedOut, setSignedOut] = useState(false);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [palette, setPalette] = useState(false);
  const server = useServer(!signedOut);

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
  useEffect(() => {
    void loadProjects();
  }, [loadProjects, current.project]);

  useEffect(() => {
    document.title = current.project ? `${current.project} · kayly` : 'kayly';
  }, [current.project]);

  if (signedOut) return <SignIn onSignedIn={loadProjects} />;

  return (
    <Shell
      projects={projects}
      server={server}
      current={current}
      navigate={navigate}
      onSearch={() => setPalette(true)}
      onSignOut={() => void api.signOut().then(() => setSignedOut(true))}
    >
      <Boundary resetKey={path}>
        {loadError ? (
          <div className="p-6">
            <p role="alert" className="text-body">
              Kayly did not answer: {loadError instanceof Error ? loadError.message : String(loadError)}. Reload to try again.
            </p>
          </div>
        ) : current.project ? (
          <ProjectPage key={current.project} name={current.project} tab={current.tab} navigate={navigate} onChanged={loadProjects} />
        ) : (
          <ProjectsPage projects={projects} server={server} navigate={navigate} onCreated={loadProjects} />
        )}
      </Boundary>
      <Palette
        open={palette}
        onOpenChange={setPalette}
        projects={projects ?? []}
        current={current}
        navigate={navigate}
        onCreated={loadProjects}
      />
    </Shell>
  );
}
