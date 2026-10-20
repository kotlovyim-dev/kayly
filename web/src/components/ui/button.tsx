import { cva, type VariantProps } from 'class-variance-authority';
import { LoaderCircle } from 'lucide-react';
import { Slot } from 'radix-ui';
import type { ComponentProps } from 'react';
import { cn } from '@/lib/utils';

const buttonVariants = cva(
  "inline-flex shrink-0 items-center justify-center gap-2 rounded-control border font-medium whitespace-nowrap transition-colors duration-150 outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-55 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default: 'border-transparent bg-primary text-primary-foreground hover:bg-primary/88',
        outline: 'border-input bg-background text-foreground hover:bg-raised',
        ghost: 'border-transparent text-foreground hover:bg-active',
        destructive: 'border-input bg-background text-negative hover:border-negative hover:bg-pill-negative',
        link: 'h-auto border-transparent px-0 text-foreground underline-offset-4 hover:underline',
      },
      size: {
        default: 'h-8 px-3 text-body',
        sm: 'h-7 px-2.5 text-small',
        lg: 'h-10 px-4 text-body',
        icon: 'size-8',
      },
    },
    defaultVariants: { variant: 'default', size: 'default' },
  },
);

type ButtonProps = ComponentProps<'button'> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean;
    /** Shows a spinner and blocks presses while an action runs. */
    busy?: boolean;
  };

function Button({ className, variant, size, asChild = false, busy = false, disabled, children, ...props }: ButtonProps) {
  const classes = cn(buttonVariants({ variant, size, className }));
  // Slot takes exactly one child, so a link styled as a button gets no spinner.
  if (asChild) {
    return (
      <Slot.Root data-slot="button" className={classes} {...props}>
        {children}
      </Slot.Root>
    );
  }
  return (
    <button data-slot="button" className={classes} disabled={disabled || busy} aria-busy={busy || undefined} {...props}>
      {busy && <LoaderCircle className="animate-spin motion-reduce:animate-none" aria-hidden="true" />}
      {children}
    </button>
  );
}

export { Button, buttonVariants };
