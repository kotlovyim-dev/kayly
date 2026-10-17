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
    <div className="flex min-h-dvh flex-col">
      <div className="h-12 bg-desk" aria-hidden="true" />
      <main className="mx-auto flex w-full max-w-[400px] flex-1 flex-col justify-center gap-8 px-4 pb-24">
        <h1 className="flex items-center gap-2.5 text-cue font-bold tracking-tight">
          <Lamp state="live" size={22} />
          kayly
        </h1>
        <form onSubmit={submit} className="flex flex-col gap-4">
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
          <Button variant="primary" type="submit" busy={busy}>
            Sign in
          </Button>
        </form>
        <div className="flex flex-col gap-2 text-meta text-ink-2">
          <p>No token yet? Create one on the server:</p>
          <Command>sudo kayly token create browser</Command>
        </div>
      </main>
    </div>
  );
}
