import { type SubmitEvent, useState } from 'react';
import { api } from './api';
import { Button, Command, ErrorNote, Field, Lamp, TextInput } from './ui';

export function SignIn({ onSignedIn }: { onSignedIn: () => Promise<void> }) {
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function submit(e: SubmitEvent<HTMLFormElement>) {
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
    <main className="flex h-dvh items-center justify-center overflow-hidden p-4">
      <div className="w-full max-w-[400px] overflow-hidden rounded-md border border-rule bg-surface">
        <h1 className="flex items-center gap-2.5 bg-display px-5 py-6 text-cue font-bold tracking-tight text-display-ink">
          <Lamp state="live" size={18} />
          kayly
        </h1>
        <form onSubmit={submit} className="flex flex-col gap-4 p-5">
          <Field label="API token">
            <TextInput
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
          </Field>
          <ErrorNote error={error} />
          <Button variant="go" type="submit" busy={busy}>
            Sign in
          </Button>
        </form>
        <div className="flex flex-col gap-2 border-t border-rule p-5 text-meta text-ink-2">
          <p>No token yet? Create one on the server:</p>
          <Command>sudo kayly token create browser</Command>
        </div>
      </div>
    </main>
  );
}
