import { Badge } from '@/components/ui/badge';
import type { Tone } from '@/lib/format';
import { cn } from '@/lib/utils';

const dotLabel: Record<Tone | 'off', string> = {
  positive: 'Live',
  warning: 'In progress',
  negative: 'Failed',
  neutral: 'Previous',
  off: 'Not running',
};

/**
 * A state mark that reads from its shape as well as its colour: filled for live, a blinking ring
 * for in progress, struck through for failed, hollow for previous, a faint dot for off.
 */
export function StatusDot({ tone, label, className }: { tone: Tone | 'off'; label?: string; className?: string }) {
  return (
    <svg viewBox="0 0 10 10" role="img" aria-label={label ?? dotLabel[tone]} className={cn('size-2.5 shrink-0', className)}>
      {tone === 'positive' && <circle cx="5" cy="5" r="4" fill="var(--positive)" />}
      {tone === 'warning' && <circle cx="5" cy="5" r="3.25" fill="none" stroke="var(--warning)" strokeWidth="1.75" className="blink" />}
      {tone === 'negative' && (
        <>
          <circle cx="5" cy="5" r="4" fill="var(--negative)" />
          <path d="M2.9 7.1 7.1 2.9" stroke="var(--background)" strokeWidth="1.3" strokeLinecap="round" />
        </>
      )}
      {tone === 'neutral' && <circle cx="5" cy="5" r="3.4" fill="none" stroke="var(--muted-foreground)" strokeWidth="1.3" />}
      {tone === 'off' && <circle cx="5" cy="5" r="2.5" fill="var(--muted-foreground)" opacity="0.45" />}
    </svg>
  );
}

/** A status word on its pale tint, led by the matching dot. */
export function StatusBadge({ tone, children }: { tone: Tone; children: string }) {
  return (
    <Badge variant={tone}>
      {tone === 'warning' && <span aria-hidden="true" className="blink size-1.5 rounded-full bg-current" />}
      {children}
    </Badge>
  );
}
