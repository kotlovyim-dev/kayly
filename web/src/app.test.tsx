// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMock } from '../mock/api';
import { App } from './App';

// Every screen renders against the same sample data `npm run dev:mock` serves.
beforeEach(() => {
  const handle = createMock({ delay: 0 });
  vi.stubGlobal('fetch', (input: string, init?: RequestInit) => handle(new Request(new URL(input, 'http://kayly'), init)));
  // jsdom lacks what Radix measures with.
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function at(path: string) {
  history.pushState(null, '', path);
  render(<App />);
}

describe('projects', () => {
  it('lists every project with its live release and the server', async () => {
    at('/');
    const table = await screen.findByRole('table');
    expect(within(table).getByText('leryo')).toBeTruthy();
    expect(within(table).getByText('r12')).toBeTruthy();
    expect(within(table).getByText('Not deployed')).toBeTruthy();
    expect(await screen.findByText('Disk used')).toBeTruthy();
    expect(screen.getByText('2 of 3', { selector: 'span' })).toBeTruthy();
  });
});

describe('project overview', () => {
  it('leads with the failed release and what kept serving', async () => {
    at('/p/leryo');
    const finding = await screen.findByText(/r12 kept serving/);
    expect(finding.textContent).toMatch(/r13 failed .*release step exited with 1/);
    expect(screen.getByRole('button', { name: 'Why r13 failed' })).toBeTruthy();
  });

  it('opens a failed release with its reason and deploy log', async () => {
    at('/p/leryo');
    fireEvent.click(await screen.findByRole('button', { name: 'Why r13 failed' }));
    const sheet = await screen.findByRole('dialog');
    expect(within(sheet).getByText(/relation "invoice_lines" already exists/)).toBeTruthy();
    expect(within(sheet).getByText('The live release, r12, kept serving.')).toBeTruthy();
    expect(await within(sheet).findByText(/docker buildx build/)).toBeTruthy();
  });

  it('rolls back only on the second press', async () => {
    at('/p/leryo');
    fireEvent.click(await screen.findByRole('button', { name: 'r11, Previous' }));
    const sheet = await screen.findByRole('dialog');
    const button = within(sheet).getByRole('button', { name: 'Roll back to r11' });
    fireEvent.click(button);
    expect(button.textContent).toBe('Go: r11 live');
    fireEvent.click(button);
    expect(await screen.findByText('Roll back to r11: r11 is live', {}, { timeout: 3000 })).toBeTruthy();
  });

  it('points a project with no release at the CLI', async () => {
    at('/p/kino-bot');
    expect(await screen.findByText('kayly deploy -p kino-bot')).toBeTruthy();
    expect(screen.getByText('Not deployed')).toBeTruthy();
  });
});

describe('settings', () => {
  it('shows add-ons, env keys, domains and backups', async () => {
    at('/p/leryo/settings');
    expect(await screen.findByText('BETTER_AUTH_SECRET')).toBeTruthy();
    expect(screen.getByText('app.leryo.eu')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Add Redis' })).toBeTruthy();
    expect((await screen.findAllByRole('link', { name: /Download backup/ })).length).toBe(4);
    expect(screen.getByText('Postgres, before r13')).toBeTruthy();
  });

  it('asks for the project name before deleting an add-on', async () => {
    at('/p/leryo/settings');
    fireEvent.click(await screen.findByRole('button', { name: 'Remove Postgres' }));
    const del = screen.getByRole('button', { name: 'Delete Postgres' }) as HTMLButtonElement;
    expect(del.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Project name'), { target: { value: 'leryo' } });
    expect(del.disabled).toBe(false);
  });
});

describe('logs', () => {
  it('streams the first service', async () => {
    at('/p/leryo/logs');
    expect(await screen.findByText(/"msg":"request completed"/)).toBeTruthy();
    expect(screen.getByRole('tab', { name: 'worker' })).toBeTruthy();
  });
});

describe('sign-in', () => {
  it('asks for a token when the session is missing', async () => {
    vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ error: { message: 'missing token' } }), { status: 401 }));
    at('/');
    expect(await screen.findByLabelText('API token')).toBeTruthy();
    expect(screen.getByText('sudo kayly token create browser')).toBeTruthy();
  });
});
