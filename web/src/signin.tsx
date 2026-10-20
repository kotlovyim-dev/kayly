import { type FormEvent, useState } from 'react';
import { api } from '@/api';
import { CommandLine } from '@/components/copy';
import { ErrorNote } from '@/components/common';
import { Mark } from '@/components/mark';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

export function SignIn({ onSignedIn }: { onSignedIn: () => Promise<void> }) {
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.signIn(token);
      await onSignedIn();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="flex min-h-dvh items-center justify-center bg-frame p-4">
      <div className="flex w-full max-w-sm flex-col gap-6">
        <div className="flex flex-col gap-1.5">
          <h1 className="flex items-center gap-2.5 text-title font-semibold">
            <Mark className="size-6" />
            Sign in to kayly
          </h1>
          <p className="text-body text-muted-foreground">{location.host}</p>
        </div>
        <form onSubmit={submit} className="flex flex-col gap-4 rounded-sheet border bg-background p-5">
          <div className="flex flex-col gap-2">
            <Label htmlFor="token">API token</Label>
            <Input
              id="token"
              type="password"
              autoComplete="current-password"
              autoFocus
              required
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="kayly_…"
              aria-invalid={error ? true : undefined}
              className="font-mono"
            />
          </div>
          <ErrorNote error={error} />
          <Button type="submit" size="lg" busy={busy}>
            Sign in
          </Button>
        </form>
        <div className="flex flex-col gap-2">
          <p className="text-body text-muted-foreground">No token yet? Create one on the server:</p>
          <CommandLine className="bg-background">sudo kayly token create browser</CommandLine>
        </div>
      </div>
    </main>
  );
}
