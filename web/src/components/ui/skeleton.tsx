import type { ComponentProps } from 'react';
import { cn } from '@/lib/utils';

/** A flat grey bar at the size of the value it stands for; no shimmer. */
function Skeleton({ className, ...props }: ComponentProps<'div'>) {
  return <div data-slot="skeleton" aria-hidden="true" className={cn('rounded-chip bg-active', className)} {...props} />;
}

export { Skeleton };
