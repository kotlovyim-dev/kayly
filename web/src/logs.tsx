import { useEffect, useRef, useState } from 'react';
import { ApiError, api } from './api';
import { ErrorNote, Sheet } from './ui';

// The output kept in the page; older lines scroll out so a chatty service cannot grow it forever.
const MAX_CHARS = 200_000;

/** Streams the output of one service of the live release, one tab per service. */
export function Logs({ name, services, releaseSeq }: { name: string; services: string[]; releaseSeq: number }) {
  const [service, setService] = useState(services[0] ?? '');
  const [text, setText] = useState('');
  const [error, setError] = useState<unknown>(null);
  const [following, setFollowing] = useState(true);
  const ref = useRef<HTMLPreElement>(null);

  useEffect(() => {
    if (!services.includes(service)) setService(services[0] ?? '');
  }, [services, service]);

  // Restart the stream when the service or the live release changes; reconnect when the
  // connection drops (a laptop waking up, a network switch), starting again from the tail.
  useEffect(() => {
    if (!service) return;
    const ac = new AbortController();
    let retry: ReturnType<typeof setTimeout> | undefined;
    const connect = () => {
      let first = true;
      api
        .logs(name, service, ac.signal, (t) => {
          if (first) {
            first = false;
            setText('');
            setError(null);
          }
          setText((prev) => (prev + t).slice(-MAX_CHARS));
        })
        .catch((e: unknown) => {
          if (ac.signal.aborted) return;
          if (e instanceof ApiError && e.status >= 400 && e.status < 500) {
            setError(e);
            return;
          }
          setError(new Error('Lost the log stream. Reconnecting…'));
          retry = setTimeout(connect, 2000);
        });
    };
    setText('');
    setError(null);
    connect();
    return () => {
      ac.abort();
      clearTimeout(retry);
    };
  }, [name, service, releaseSeq]);

  useEffect(() => {
    const el = ref.current;
    if (el && following) el.scrollTop = el.scrollHeight;
  }, [text, following]);

  return (
    <Sheet
      id="logs"
      title="Logs"
      action={
        services.length > 1 && (
          <div role="tablist" aria-label="Service" className="flex rounded-md border border-rule p-0.5">
            {services.map((s) => (
              <button
                key={s}
                role="tab"
                type="button"
                aria-selected={s === service}
                onClick={() => setService(s)}
                className={`h-7 rounded px-2.5 text-meta font-semibold transition-colors duration-150 ${
                  s === service ? 'bg-plate text-plate-ink' : 'text-ink-2 hover:bg-hover hover:text-ink'
                }`}
              >
                {s}
              </button>
            ))}
          </div>
        )
      }
    >
      {error ? (
        <div className="border-b border-rule px-4 py-2">
          <ErrorNote error={error} />
        </div>
      ) : null}
      {!(error instanceof ApiError) && (
        <pre
          ref={ref}
          tabIndex={0}
          role="log"
          aria-label={`Output of ${service}`}
          onScroll={(e) => {
            const el = e.currentTarget;
            setFollowing(el.scrollHeight - el.scrollTop - el.clientHeight < 24);
          }}
          className="h-80 overflow-auto rounded-b-lg bg-code px-4 py-3 font-mono text-meta whitespace-pre-wrap text-ink"
        >
          {text || `No output from ${service} yet.`}
        </pre>
      )}
    </Sheet>
  );
}
