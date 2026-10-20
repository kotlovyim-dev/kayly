import { Check, Copy } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

export function CopyButton({ text, label, className }: { text: string; label: string; className?: string }) {
  const [done, setDone] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  return (
    <Button
      variant="ghost"
      size="icon"
      aria-label={done ? 'Copied' : label}
      title={done ? 'Copied' : label}
      className={cn('size-7 text-muted-foreground hover:text-foreground', className)}
      onClick={() =>
        void navigator.clipboard.writeText(text).then(() => {
          setDone(true);
          clearTimeout(timer.current);
          timer.current = setTimeout(() => setDone(false), 1500);
        })
      }
    >
      {done ? <Check className="text-positive" /> : <Copy />}
    </Button>
  );
}

/** A command to run in a terminal: the next step when something is the CLI's job. */
export function CommandLine({ children, className }: { children: string; className?: string }) {
  return (
    <div className={cn('flex h-9 items-center gap-2 rounded-control bg-raised pr-1 pl-3', className)}>
      <code className="min-w-0 flex-1 overflow-x-auto font-mono text-small whitespace-nowrap">{children}</code>
      <CopyButton text={children} label="Copy command" />
    </div>
  );
}
