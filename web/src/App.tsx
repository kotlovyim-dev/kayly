import { LogOut } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import { ApiError, api, type Project } from './api';
import { ProjectPage } from './project';
import { ProjectsPage } from './projects';
import { SignIn } from './signin';
import { Lamp } from './ui';

export type Navigate = (path: string) => void;

function projectFromPath(path: string): string | null {
  const m = /^\/p\/([^/]+)\/?$/.exec(path);
  return m ? decodeURIComponent(m[1]) : null;
}

export function App() {
  const [path, setPath] = useState(location.pathname);
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [signedOut, setSignedOut] = useState(false);
  const [loadError, setLoadError] = useState<unknown>(null);

  const navigate: Navigate = useCallback((to) => {
    history.pushState(null, '', to);
    setPath(to);
    window.scrollTo(0, 0);
  }, []);

  useEffect(() => {
    const onPop = () => setPath(location.pathname);
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
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

  useEffect(() => {
    void loadProjects();
  }, [loadProjects, path]);

  const current = projectFromPath(path);
  useEffect(() => {
    document.title = current ? `${current} · kayly` : 'kayly';
  }, [current]);

  if (signedOut) return <SignIn onSignedIn={loadProjects} />;

  return (
    <div className="flex min-h-dvh flex-col">
      <header className="sticky top-0 z-10 bg-desk text-desk-ink">
        <nav aria-label="Main" className="mx-auto flex h-12 max-w-[1440px] items-center gap-4 px-4 sm:px-6">
          <a
            href="/"
            onClick={(e) => {
              e.preventDefault();
              navigate('/');
            }}
            className="flex items-center gap-2 rounded text-title font-bold tracking-tight text-desk-ink no-underline"
          >
            <Lamp state="live" size={14} />
            kayly
          </a>
          {current && projects && projects.length > 1 && (
            <label className="flex min-w-0 items-center gap-2">
              <span className="sr-only">Project</span>
              <select
                value={current}
                onChange={(e) => navigate('/p/' + encodeURIComponent(e.target.value))}
                className="h-8 min-w-0 cursor-pointer rounded-md border border-desk-ink/20 bg-desk px-2 text-body text-desk-ink hover:border-desk-ink/40"
              >
                {projects.map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
          )}
          <button
            type="button"
            onClick={() => void api.signOut().then(() => setSignedOut(true))}
            className="ml-auto inline-flex h-8 items-center gap-2 rounded-md px-2 text-body text-desk-ink-2 transition-colors duration-150 hover:bg-desk-ink/10 hover:text-desk-ink"
          >
            <LogOut size={16} aria-hidden="true" />
            <span className="max-sm:sr-only">Sign out</span>
          </button>
        </nav>
      </header>

      <main className="mx-auto w-full max-w-[1440px] flex-1 px-4 pt-5 pb-12 sm:px-6">
        {loadError ? (
          <p role="alert" className="text-fail-ink">
            Kayly did not answer: {loadError instanceof Error ? loadError.message : String(loadError)}. Reload to try again.
          </p>
        ) : current ? (
          <ProjectPage key={current} name={current} navigate={navigate} onChanged={loadProjects} />
        ) : (
          <ProjectsPage projects={projects} navigate={navigate} onCreated={loadProjects} />
        )}
      </main>
    </div>
  );
}
