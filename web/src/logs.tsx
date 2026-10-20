import { useEffect, useRef, useState } from 'react';
import { ApiError, api } from '@/api';
import { ErrorNote } from '@/components/common';
import { StatusDot } from '@/components/status';
import { cn } from '@/lib/utils';

// The output kept in the page; older lines scroll out so a chatty service cannot grow it forever.
const MAX_CHARS = 200_000;

/** Streams the output of one service of the live release. */
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
    <section aria-labelledby="logs" className="flex h-[calc(100dvh-15rem)] min-h-80 flex-col rounded-card border lg:h-[calc(100dvh-14rem)]">
      <header className="flex min-h-12 flex-wrap items-center gap-3 border-b px-4 py-2">
        <h2 id="logs" className="text-body font-semibold">
          Output of <span className="font-mono">{service}</span>
        </h2>
        <span className="flex items-center gap-1.5 text-small text-muted-foreground" aria-live="polite">
          <StatusDot tone={following ? 'positive' : 'off'} label="" />
          {following ? `Following r${releaseSeq}` : 'Paused while you scroll'}
        </span>
        {services.length > 1 && (
          <div role="tablist" aria-label="Service" className="ml-auto flex h-8 rounded-control bg-raised p-0.5">
            {services.map((s) => (
              <button
                key={s}
                role="tab"
                type="button"
                aria-selected={s === service}
                onClick={() => setService(s)}
                className={cn(
                  'rounded-[5px] border px-3 text-body transition-colors duration-150',
                  s === service ? 'border-border bg-background font-medium' : 'border-transparent text-muted-foreground hover:text-foreground',
                )}
              >
                {s}
              </button>
            ))}
          </div>
        )}
      </header>
      {error ? <ErrorNote error={error} className="border-b px-4 py-2.5" /> : null}
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
          className="min-h-0 flex-1 overflow-auto rounded-b-card bg-raised px-4 py-3 font-mono text-small whitespace-pre-wrap"
        >
          {text || `No output from ${service} yet.`}
        </pre>
      )}
    </section>
  );
}
