import { cn } from '@/lib/utils';

/**
 * Kayly's mark: three releases as steps, the newest one live in the data hue. It is the release
 * track (release-track.tsx) drawn small.
 */
export function Mark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true" className={cn('size-5 shrink-0', className)}>
      <rect x="1.5" y="11" width="4.5" height="7.5" rx="1" fill="none" stroke="currentColor" strokeWidth="1.5" opacity="0.45" />
      <rect x="7.75" y="7" width="4.5" height="11.5" rx="1" fill="none" stroke="currentColor" strokeWidth="1.5" opacity="0.7" />
      <rect x="14" y="1.5" width="4.5" height="17" rx="1" fill="var(--chart-1)" />
    </svg>
  );
}
