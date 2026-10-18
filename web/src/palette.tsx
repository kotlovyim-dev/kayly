import { Plus, Search } from 'lucide-react';
import { type ReactNode, useEffect, useId, useRef, useState } from 'react';
import { type Navigate, projectPath } from './App';
import { api, type Project } from './api';
import type { Tab } from './project';
import { Lamp } from './ui';

export const modKey = /Mac|iP(hone|ad)/.test(navigator.userAgent) ? '⌘' : 'Ctrl ';

const validName = /^[a-z][a-z0-9-]{0,30}$/;

type Item = { id: string; label: ReactNode; meta?: ReactNode; icon: ReactNode; run: () => void | Promise<void> };

/** The switcher: jump to a project or a tab, or create a project, from the keyboard. */
export function Palette({
  projects,
  current,
  navigate,
  onCreated,
  onClose,
}: {
  projects: Project[];
  current: { project: string; tab: Tab } | null;
  navigate: Navigate;
  onCreated: () => Promise<void>;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const [error, setError] = useState<unknown>(null);
  const listId = useId();

  useEffect(() => {
    ref.current?.showModal();
  }, []);

  const go = (to: string) => {
    navigate(to);
    onClose();
  };
  const q = query.trim().toLowerCase();
  const tab = current?.tab ?? 'overview';

  const items: Item[] = [
    ...projects
      .filter((p) => p.name.includes(q))
      .map((p) => ({
        id: 'p-' + p.name,
        label: p.name,
        meta: p.live_release ? `r${p.live_release}` : 'not deployed',
        icon: <Lamp state={p.live_release ? 'live' : 'off'} />,
        run: () => go(projectPath(p.name, tab)),
      })),
    ...(current
      ? (['overview', 'logs', 'settings'] as const)
          .filter((t) => t.includes(q))
          .map((t, i) => ({
            id: 't-' + t,
            label: t[0].toUpperCase() + t.slice(1),
            meta: `${current.project} · ${i + 1}`,
            icon: <span className="size-3" />,
            run: () => go(projectPath(current.project, t)),
          }))
      : []),
    ...(validName.test(q) && !projects.some((p) => p.name === q)
      ? [
          {
            id: 'create',
            label: (
              <>
                Create project <span className="font-mono">{q}</span>
              </>
            ),
            icon: <Plus size={12} />,
            run: async () => {
              try {
                const p = await api.createProject(q);
                await onCreated();
                go(projectPath(p.name));
              } catch (e) {
                setError(e);
              }
            },
          },
        ]
      : []),
  ];
  const sel = Math.min(active, items.length - 1);

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      onClick={(e) => e.target === ref.current && ref.current.close()}
      aria-label="Switch project"
      className="m-auto mt-[12vh] w-[min(560px,calc(100vw-32px))] overflow-hidden rounded-md border border-display-rule bg-display p-0 text-display-ink"
    >
      <div className="flex h-12 items-center gap-3 border-b border-display-rule px-4">
        <Search size={16} aria-hidden="true" className="shrink-0 text-display-ink-2" />
        <input
          autoFocus
          role="combobox"
          aria-expanded="true"
          aria-controls={listId}
          aria-activedescendant={items[sel] ? `${listId}-${items[sel].id}` : undefined}
          aria-label="Project, tab or new project name"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
            setError(null);
          }}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown') {
              e.preventDefault();
              setActive((sel + 1) % items.length);
            } else if (e.key === 'ArrowUp') {
              e.preventDefault();
              setActive((sel - 1 + items.length) % items.length);
            } else if (e.key === 'Enter' && items[sel]) {
              e.preventDefault();
              void items[sel].run();
            }
          }}
          placeholder="Jump to a project, or type a new name"
          className="h-full min-w-0 flex-1 bg-transparent text-body text-display-ink outline-none placeholder:text-display-ink-2"
        />
      </div>
      <ul id={listId} role="listbox" aria-label="Results" className="max-h-[50vh] overflow-y-auto p-1.5">
        {items.map((it, i) => (
          <li
            key={it.id}
            id={`${listId}-${it.id}`}
            role="option"
            aria-selected={i === sel}
            onMouseMove={() => setActive(i)}
            onClick={() => void it.run()}
            className={`flex h-10 cursor-pointer items-center gap-3 rounded-[3px] px-3 ${
              i === sel ? 'bg-select/30 text-display-ink' : 'text-display-ink'
            }`}
          >
            <span className="flex w-3 justify-center">{it.icon}</span>
            <span className="min-w-0 flex-1 truncate font-semibold">{it.label}</span>
            {it.meta && (
              <span className={`font-mono text-meta ${'text-display-ink-2'}`}>{it.meta}</span>
            )}
          </li>
        ))}
        {items.length === 0 && (
          <li className="px-3 py-2.5 text-display-ink-2">
            Nothing matches. A new project name uses lowercase letters, digits and dashes.
          </li>
        )}
      </ul>
      {error ? (
        <p role="alert" className="border-t border-display-rule px-4 py-2.5 text-meta font-semibold text-fail">
          {error instanceof Error ? error.message : String(error)}
        </p>
      ) : null}
    </dialog>
  );
}
