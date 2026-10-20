import { type ComponentProps, type ReactNode, useEffect, useState } from 'react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

/**
 * Standby, then go: the first press arms the action and rewrites the label to say exactly what
 * will happen ("Go: r11 live"); a second press within four seconds runs it. Escape, blur or the
 * timeout stands it down. Used for everything that changes what is live or deletes a value.
 */
export function ArmButton({
  label,
  go,
  onGo,
  busy = false,
  variant = 'outline',
  size,
  className,
}: {
  label: ReactNode;
  go: string;
  onGo: () => void;
  busy?: boolean;
  variant?: ComponentProps<typeof Button>['variant'];
  size?: ComponentProps<typeof Button>['size'];
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

  return (
    <Button
      type="button"
      variant={armed ? 'outline' : variant}
      size={size}
      busy={busy}
      aria-live="polite"
      onBlur={() => setArmed(false)}
      onClick={() => {
        if (!armed) return setArmed(true);
        setArmed(false);
        onGo();
      }}
      className={cn(
        armed && 'border-pill-warning-edge bg-pill-warning text-pill-warning-ink hover:bg-pill-warning',
        className,
      )}
    >
      {armed && <span aria-hidden="true" className="blink size-1.5 rounded-full bg-current" />}
      {armed ? go : label}
    </Button>
  );
}
