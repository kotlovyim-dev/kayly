import type { ComponentProps } from 'react';
import { cn } from '@/lib/utils';

function Input({ className, type, ...props }: ComponentProps<'input'>) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        'h-9 w-full min-w-0 rounded-control border border-border bg-background px-3 text-body text-foreground transition-colors duration-150 outline-none placeholder:text-muted-foreground hover:border-input focus-visible:border-ring focus-visible:outline-2 focus-visible:outline-offset-0 focus-visible:outline-ring/30 disabled:cursor-not-allowed disabled:opacity-55 aria-invalid:border-negative',
        className,
      )}
      {...props}
    />
  );
}

export { Input };
