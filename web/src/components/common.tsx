import { CircleAlert } from 'lucide-react';
import type { ReactNode } from 'react';
import { ApiError } from '@/api';
import { relativeTime, splitCode } from '@/lib/format';
import { cn } from '@/lib/utils';

/** An error in the interface's voice: what happened, then what to do. */
export function ErrorNote({ error, className }: { error: unknown; className?: string }) {
  if (!error) return null;
  const hint = error instanceof ApiError ? error.hint : undefined;
  const message = error instanceof Error ? error.message : String(error);
  return (
    <div role="alert" className={cn('flex gap-2 text-body', className)}>
      <CircleAlert className="mt-0.5 size-4 shrink-0 text-negative" aria-hidden="true" />
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="font-medium">
          <Inline text={message} />
        </span>
        {hint && (
          <span className="text-muted-foreground">
            <Inline text={hint} />
          </span>
        )}
      </div>
    </div>
  );
}

/** Kayly's messages with their `quoted` commands set as code. */
export function Inline({ text }: { text: string }) {
  return (
    <>
      {splitCode(text).map((p, i) =>
        p.code ? (
          <code key={i} className="font-mono text-[0.92em]">
            {p.text}
          </code>
        ) : (
          p.text
        ),
      )}
    </>
  );
}

/** A relative time with the exact time on hover. */
export function When({ at }: { at: string }) {
  return (
    <time dateTime={at} title={new Date(at).toLocaleString()}>
      {relativeTime(at)}
    </time>
  );
}

export const modKey = typeof navigator !== 'undefined' && /Mac|iP(hone|ad)/.test(navigator.userAgent) ? '⌘' : 'Ctrl ';

/** A keyboard shortcut hint. */
export function Kbd({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <kbd className={cn('inline-flex h-5 items-center rounded-chip border bg-background px-1.5 font-sans text-small text-muted-foreground', className)}>
      {children}
    </kbd>
  );
}

/**
 * A titled block on the sheet: one 1px edge, a header row with an optional action, and a body.
 * Its content keeps its own height; nothing is stretched over empty space.
 */
export function Panel({
  title,
  meta,
  action,
  children,
  id,
  className,
}: {
  title: ReactNode;
  meta?: ReactNode;
  action?: ReactNode;
  children: ReactNode;
  id: string;
  className?: string;
}) {
  return (
    <section aria-labelledby={id} className={cn('flex min-w-0 flex-col rounded-card border bg-background', className)}>
      <header className="flex min-h-12 items-center gap-3 border-b px-4">
        <h2 id={id} className="text-body font-semibold">
          {title}
        </h2>
        {meta && <span className="text-small text-muted-foreground">{meta}</span>}
        {action && <div className="ml-auto flex items-center gap-2">{action}</div>}
      </header>
      {children}
    </section>
  );
}

/** One sentence where a block has nothing yet, keeping the block's frame. */
export function Empty({ children }: { children: ReactNode }) {
  return <p className="px-4 py-3.5 text-body text-muted-foreground">{children}</p>;
}
