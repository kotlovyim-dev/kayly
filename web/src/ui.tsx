import { Check, Copy, LoaderCircle } from 'lucide-react';
import {
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  useEffect,
  useRef,
  useState,
} from 'react';
import { ApiError } from './api';

export type LampState = 'live' | 'standby' | 'failed' | 'superseded' | 'off';

const lampLabel: Record<LampState, string> = {
  live: 'Live',
  standby: 'Standing by',
  failed: 'Failed',
  superseded: 'Previous',
  off: 'Not running',
};

/**
 * A cue lamp. State is carried by shape as well as colour: filled for live, a ring for
 * standby, struck through for failed, hollow for superseded.
 */
export function Lamp({ state, arrive = false, size = 12 }: { state: LampState; arrive?: boolean; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 12 12" role="img" aria-label={lampLabel[state]} className="shrink-0">
      {state === 'live' && (
        <>
          <circle cx="6" cy="6" r="5.5" fill="var(--k-go)" opacity="0.22" />
          <circle cx="6" cy="6" r="3.75" fill="var(--k-go)" className={arrive ? 'lamp-go-arrive' : undefined} />
        </>
      )}
      {state === 'standby' && (
        <circle
          cx="6"
          cy="6"
          r="4.25"
          fill="none"
          stroke="var(--k-standby)"
          strokeWidth="2.5"
          className="lamp-standby-blink"
        />
      )}
      {state === 'failed' && (
        <>
          <circle cx="6" cy="6" r="4.5" fill="var(--k-fail)" />
          <path d="M3.4 8.6 8.6 3.4" stroke="var(--k-sheet)" strokeWidth="1.6" strokeLinecap="round" />
        </>
      )}
      {state === 'superseded' && <circle cx="6" cy="6" r="4" fill="none" stroke="currentColor" strokeWidth="1.5" opacity="0.55" />}
      {state === 'off' && <circle cx="6" cy="6" r="4.25" fill="currentColor" opacity="0.28" />}
    </svg>
  );
}

// Spike tape colours, as stage managers mark set positions on the floor.
const tape = ['#ff3d8b', '#f5d800', '#38c96a', '#2f7bff', '#ff7a1a', '#9b5cff', '#00b8c4'];

/** A short strip of spike tape that tells projects apart at a glance. */
export function SpikeStrip({ name, className = '' }: { name: string; className?: string }) {
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return (
    <span
      aria-hidden="true"
      className={`inline-block h-[7px] w-[22px] shrink-0 -rotate-6 rounded-[1px] ${className}`}
      style={{ background: tape[h % tape.length] }}
    />
  );
}

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'quiet' | 'danger';
  busy?: boolean;
};

export function Button({ variant = 'quiet', busy = false, className = '', children, disabled, ...rest }: ButtonProps) {
  const look = {
    primary: 'bg-action text-action-ink hover:bg-action-hover border-transparent',
    quiet: 'bg-sheet text-ink border-rule hover:bg-hover',
    danger: 'bg-sheet text-fail-ink border-rule hover:border-fail hover:bg-hover',
  }[variant];
  return (
    <button
      {...rest}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      className={`inline-flex h-9 items-center justify-center gap-2 rounded-md border px-3.5 text-body font-semibold whitespace-nowrap transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-55 ${look} ${className}`}
    >
      {busy && <LoaderCircle size={16} className="animate-spin motion-reduce:animate-none" aria-hidden="true" />}
      {children}
    </button>
  );
}

/** An icon-only button with an accessible name. */
export function IconButton({ label, children, className = '', ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return (
    <button
      {...rest}
      aria-label={label}
      title={label}
      className={`inline-flex size-8 shrink-0 items-center justify-center rounded-md text-ink-2 transition-colors duration-150 hover:bg-hover hover:text-ink disabled:opacity-50 ${className}`}
    >
      {children}
    </button>
  );
}

/**
 * The stage manager's call: the first press stands the action by (the lamp blinks amber),
 * the second press within a few seconds makes it go. Escape or a timeout stands it down.
 */
export function StandbyGo({
  standby,
  go,
  onGo,
  busy = false,
  variant = 'quiet',
  small = false,
  className = '',
}: {
  standby: string;
  go: string;
  onGo: () => void;
  busy?: boolean;
  variant?: 'primary' | 'quiet' | 'danger';
  small?: boolean;
  className?: string;
}) {
  const [armed, setArmed] = useState(false);
  useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), 4000);
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setArmed(false);
    window.addEventListener('keydown', esc);
    return () => {
      clearTimeout(t);
      window.removeEventListener('keydown', esc);
    };
  }, [armed]);

  if (busy) {
    return (
      <Button variant={variant} busy className={`${small ? 'h-8 px-2.5 text-meta' : ''} ${className}`}>
        {standby}
      </Button>
    );
  }
  return (
    <button
      type="button"
      onClick={() => {
        if (armed) {
          setArmed(false);
          onGo();
        } else setArmed(true);
      }}
      onBlur={() => setArmed(false)}
      aria-live="polite"
      className={`inline-flex items-center justify-center gap-2 rounded-md border font-semibold whitespace-nowrap transition-colors duration-150 ${
        small ? 'h-8 px-2.5 text-meta' : 'h-9 px-3.5 text-body'
      } ${
        armed
          ? 'border-standby bg-standby/15 text-ink'
          : {
              primary: 'border-transparent bg-action text-action-ink hover:bg-action-hover',
              quiet: 'border-rule bg-sheet text-ink hover:bg-hover',
              danger: 'border-rule bg-sheet text-fail-ink hover:border-fail hover:bg-hover',
            }[variant]
      } ${className}`}
    >
      {armed && <Lamp state="standby" />}
      {armed ? go : standby}
    </button>
  );
}

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="flex min-w-0 flex-col gap-1">
      <span className="text-meta font-semibold text-ink-2">{label}</span>
      {children}
      {hint && <span className="text-meta text-ink-2">{hint}</span>}
    </label>
  );
}

const control =
  'h-9 w-full min-w-0 rounded-md border border-rule bg-sheet px-3 text-body text-ink placeholder:text-ink-2 transition-colors duration-150 hover:border-ink-2/60 focus:border-focus focus:outline-none focus-visible:outline-2 focus-visible:outline-focus/40 aria-[invalid=true]:border-fail';

export function TextInput({ className = '', ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...rest} className={`${control} ${className}`} />;
}

export function Select({ className = '', children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select {...rest} className={`${control} cursor-pointer pr-8 ${className}`}>
      {children}
    </select>
  );
}

/** A titled work area on a white sheet. */
export function Sheet({
  title,
  action,
  children,
  id,
  quiet = false,
  className = '',
}: {
  title: string;
  action?: ReactNode;
  children: ReactNode;
  id?: string;
  /** A ruled section on the floor instead of a white sheet, for supporting areas. */
  quiet?: boolean;
  className?: string;
}) {
  return (
    <section
      aria-labelledby={id}
      className={`${quiet ? 'border-t border-rule' : 'rounded-lg border border-rule bg-sheet'} ${className}`}
    >
      <header className="flex min-h-12 items-center justify-between gap-3 border-b border-rule px-4 py-2">
        <h2 id={id} className="text-body font-bold">
          {title}
        </h2>
        {action}
      </header>
      {children}
    </section>
  );
}

/** A command the visitor runs in a terminal, with a copy button. */
export function Command({ children }: { children: string }) {
  return (
    <div className="flex items-center gap-2 rounded-md border border-rule bg-code py-1 pr-1 pl-3">
      <code className="min-w-0 flex-1 overflow-x-auto font-mono text-meta whitespace-nowrap">{children}</code>
      <CopyButton text={children} label="Copy command" />
    </div>
  );
}

export function CopyButton({ text, label }: { text: string; label: string }) {
  const [done, setDone] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  return (
    <IconButton
      label={done ? 'Copied' : label}
      onClick={() => {
        void navigator.clipboard.writeText(text).then(() => {
          setDone(true);
          clearTimeout(timer.current);
          timer.current = setTimeout(() => setDone(false), 1500);
        });
      }}
    >
      {done ? <Check size={16} className="text-go-ink" /> : <Copy size={16} />}
    </IconButton>
  );
}

/** An error in the interface's voice: what happened, then what to do. */
export function ErrorNote({ error }: { error: unknown }) {
  if (!error) return null;
  const e = error instanceof ApiError ? error : null;
  const message = error instanceof Error ? error.message : String(error);
  return (
    <div role="alert" className="flex flex-col gap-0.5 text-meta">
      <span className="font-semibold text-fail-ink">{message}</span>
      {e?.hint && <span className="text-ink-2">{e.hint}</span>}
    </div>
  );
}

const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 31536000],
  ['month', 2592000],
  ['day', 86400],
  ['hour', 3600],
  ['minute', 60],
];
const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });

/** A relative time with the exact time on hover. */
export function When({ at }: { at: string }) {
  const d = new Date(at);
  const s = (d.getTime() - Date.now()) / 1000;
  let text = 'just now';
  for (const [unit, secs] of units) {
    if (Math.abs(s) >= secs) {
      text = rtf.format(Math.round(s / secs), unit);
      break;
    }
  }
  return (
    <time dateTime={at} title={d.toLocaleString()}>
      {text}
    </time>
  );
}

export function formatBytes(n: number) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
