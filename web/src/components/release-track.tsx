import type { Release } from '@/api';
import { releaseStatus } from '@/lib/format';
import { cn } from '@/lib/utils';

function stepLook(r: Release) {
  switch (r.status) {
    case 'live':
      return 'border-chart-1 bg-chart-1';
    case 'failed':
      return 'border-negative';
    case 'superseded':
      return r.can_roll_back ? 'border-muted-foreground' : 'border-input';
    default:
      return 'blink border-warning';
  }
}

// How many steps fit: the newest ones. Older releases stay in the releases list.
const fit = 'max-sm:[&:nth-last-child(n+11)]:hidden max-xl:[&:nth-last-child(n+21)]:hidden';

/**
 * The release track: one step per release, oldest to newest, the live one filled in the data hue,
 * failed ones struck, previous ones hollow (darker while their image is kept for rollback).
 * Choosing a step opens that release; arrow keys move along the track.
 */
export function ReleaseTrack({
  releases,
  landed,
  onOpen,
}: {
  releases: Release[];
  /** The release that just went live, whose step lands. */
  landed: number | null;
  onOpen: (seq: number) => void;
}) {
  const steps = [...releases].sort((a, b) => a.seq - b.seq).slice(-32);
  return (
    <ol
      aria-label="Releases, oldest to newest"
      className="flex flex-wrap gap-1"
      onKeyDown={(e) => {
        const d = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
        const at = steps.findIndex((r) => r.seq === Number((document.activeElement as HTMLElement | null)?.dataset.seq));
        const next = steps[at + d];
        if (!d || !next) return;
        e.preventDefault();
        (e.currentTarget.querySelector(`[data-seq="${next.seq}"]`) as HTMLElement | null)?.focus();
      }}
    >
      {steps.map((r, i) => (
        <li key={r.seq} className={cn('flex w-6 flex-col items-center gap-1', fit)}>
          <button
            type="button"
            data-seq={r.seq}
            tabIndex={i === steps.length - 1 ? 0 : -1}
            aria-label={`r${r.seq}, ${releaseStatus[r.status].word}`}
            title={`r${r.seq} · ${releaseStatus[r.status].word}`}
            onClick={() => onOpen(r.seq)}
            className={cn(
              'relative size-5 rounded-chip border-[1.5px] transition-transform duration-150 hover:scale-110',
              stepLook(r),
              r.status === 'live' && landed === r.seq && 'step-land',
            )}
          >
            {r.status === 'failed' && (
              <svg viewBox="0 0 20 20" aria-hidden="true" className="absolute inset-0 size-full">
                <path d="M5 15 15 5" stroke="var(--negative)" strokeWidth="2" strokeLinecap="round" />
              </svg>
            )}
          </button>
          <span aria-hidden="true" className={cn('font-mono text-small', r.status === 'live' ? 'text-foreground' : 'text-muted-foreground')}>
            {r.seq}
          </span>
        </li>
      ))}
    </ol>
  );
}
