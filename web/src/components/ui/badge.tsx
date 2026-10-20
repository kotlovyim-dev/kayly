import { cva, type VariantProps } from 'class-variance-authority';
import type { ComponentProps } from 'react';
import { cn } from '@/lib/utils';

// A pale tint with dark ink of the same hue: state beside a value, never decoration.
const badgeVariants = cva(
  'inline-flex h-5 flex-none items-center gap-1.5 rounded-chip px-1.5 text-small font-medium whitespace-nowrap',
  {
    variants: {
      variant: {
        positive: 'bg-pill-positive text-pill-positive-ink',
        negative: 'bg-pill-negative text-pill-negative-ink',
        warning: 'bg-pill-warning text-pill-warning-ink',
        neutral: 'bg-pill-neutral text-pill-neutral-ink',
      },
    },
    defaultVariants: { variant: 'neutral' },
  },
);

function Badge({ className, variant, ...props }: ComponentProps<'span'> & VariantProps<typeof badgeVariants>) {
  return <span data-slot="badge" className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export { Badge };
