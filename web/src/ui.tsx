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
  standby: 'In progress',
  failed: 'Failed',
  superseded: 'Previous',
  off: 'Not running',
};

/**
 * A state light. State is carried by shape as well as colour: filled for live, a ring for
 * in progress, struck through for failed, hollow for previous, a faint dot for off.
 */
export function Lamp({ state, land = false, size = 12 }: { state: LampState; land?: boolean; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 12 12" role="img" aria-label={lampLabel[state]} className="shrink-0">
      {state === 'live' && (
        <>
          <circle cx="6" cy="6" r="5.5" fill="var(--k-live)" opacity="0.25" />
          <circle cx="6" cy="6" r="3.75" fill="var(--k-live)" className={land ? 'light-land' : undefined} />
        </>
      )}
      {state === 'standby' && (
        <circle cx="6" cy="6" r="4.25" fill="none" stroke="var(--k-busy)" strokeWidth="2.5" className="light-blink" />
      )}
      {state === 'failed' && (
        <>
          <circle cx="6" cy="6" r="4.5" fill="var(--k-fail)" />
          <path d="M3.4 8.6 8.6 3.4" stroke="var(--k-surface)" strokeWidth="1.6" strokeLinecap="round" />
        </>
      )}
      {state === 'superseded' && <circle cx="6" cy="6" r="4" fill="none" stroke="currentColor" strokeWidth="1.5" opacity="0.6" />}
      {state === 'off' && <circle cx="6" cy="6" r="3" fill="currentColor" opacity="0.3" />}
    </svg>
  );
}

type Variant = 'go' | 'quiet' | 'danger' | 'display';

const look: Record<Variant, string> = {
  go: 'border-transparent bg-go text-go-ink hover:bg-go-hover',
  quiet: 'border-rule bg-surface text-ink hover:bg-hover',
  danger: 'border-rule bg-surface text-fail-ink hover:border-fail hover:bg-hover',
  display: 'border-display-rule bg-display text-display-ink hover:bg-display-hover hover:border-display-ink-2',
};

const size = { md: 'h-9 px-3.5 text-body', sm: 'h-8 px-2.5 text-meta' };

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: Variant;
  small?: boolean;
  busy?: boolean;
};

export function Button({ variant = 'quiet', small = false, busy = false, className = '', children, disabled, ...rest }: ButtonProps) {
  return (
    <button
      {...rest}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      className={`inline-flex items-center justify-center gap-2 rounded-[3px] border font-semibold whitespace-nowrap transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-55 ${size[small ? 'sm' : 'md']} ${look[variant]} ${className}`}
    >
      {busy && <LoaderCircle size={14} className="animate-spin motion-reduce:animate-none" aria-hidden="true" />}
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
      className={`inline-flex size-8 shrink-0 items-center justify-center rounded-[3px] text-ink-2 transition-colors duration-150 hover:bg-hover hover:text-ink disabled:opacity-50 ${className}`}
    >
      {children}
    </button>
  );
}

/**
 * Arm, then go: the first press arms the action and lights the orange key, the second press
 * within a few seconds runs it. Escape, blur or a timeout disarms.
 */
export function ArmGo({
  label,
  go,
  onGo,
  busy = false,
  variant = 'quiet',
  small = false,
  className = '',
}: {
  label: ReactNode;
  go: string;
  onGo: () => void;
  busy?: boolean;
  variant?: Variant;
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
      <Button variant={variant} small={small} busy className={className}>
        {label}
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
      className={`inline-flex items-center justify-center gap-2 rounded-[3px] border font-semibold whitespace-nowrap transition-colors duration-150 ${size[small ? 'sm' : 'md']} ${
        armed ? look.go : look[variant]
      } ${className}`}
    >
      {armed && <span aria-hidden="true" className="light-blink size-1.5 rounded-full bg-go-ink" />}
      {armed ? go : label}
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
  'h-9 w-full min-w-0 rounded-[3px] border border-rule bg-surface px-3 text-body text-ink placeholder:text-ink-2/70 transition-colors duration-150 hover:border-ink-2/60 focus:border-select focus:outline-none aria-[invalid=true]:border-fail';

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

/**
 * A titled work area that fills the space it is given. Its body scrolls on its own, so the
 * window never has to.
 */
export function Pane({
  title,
  action,
  children,
  id,
  scroll = true,
  className = '',
}: {
  title: ReactNode;
  action?: ReactNode;
  children: ReactNode;
  id?: string;
  /** False when the content scrolls itself, such as a log. */
  scroll?: boolean;
  className?: string;
}) {
  return (
    <section aria-labelledby={id} className={`flex min-h-0 flex-col rounded-md border border-rule bg-surface ${className}`}>
      <header className="flex min-h-11 shrink-0 items-center justify-between gap-3 border-b border-rule px-4">
        <h2 id={id} className="text-body font-semibold">
          {title}
        </h2>
        {action}
      </header>
      <div className={`flex min-h-0 flex-1 flex-col ${scroll ? 'overflow-y-auto' : ''}`}>{children}</div>
    </section>
  );
}

/** A command the visitor runs in a terminal, with a copy button. */
export function Command({ children }: { children: string }) {
  return (
    <div className="flex items-center gap-2 rounded-[3px] border border-rule bg-code py-1 pr-1 pl-3">
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
      {done ? <Check size={16} className="text-live-ink" /> : <Copy size={16} />}
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

/** A keyboard shortcut hint. */
export function Kbd({ children, className = '' }: { children: ReactNode; className?: string }) {
  return (
    <kbd className={`rounded-[3px] border border-current/25 px-1 font-mono text-meta ${className}`}>
      {children}
    </kbd>
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
