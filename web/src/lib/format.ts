import type { ReleaseStatus, Service } from '@/api';

const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 31536000],
  ['month', 2592000],
  ['day', 86400],
  ['hour', 3600],
  ['minute', 60],
];
const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });

/** "38 minutes ago", "yesterday", "just now". */
export function relativeTime(at: string, now = Date.now()): string {
  const s = (new Date(at).getTime() - now) / 1000;
  for (const [unit, secs] of units) {
    if (Math.abs(s) >= secs) return rtf.format(Math.round(s / secs), unit);
  }
  return 'just now';
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`;
  return `${(n / 1024 ** 3).toFixed(1)} GB`;
}

/** "3a77de6" for a commit, with a star when the work tree had uncommitted changes. */
export function shortCommit(commit: string): string {
  const [sha, dirty] = commit.endsWith('-dirty') ? [commit.slice(0, -6), true] : [commit, false];
  return sha.slice(0, 7) + (dirty ? '*' : '');
}

/**
 * What a backup holds and why it was taken. A backup is named <time>-<reason>.<kind>, the kind
 * being dump (Postgres), rdb (Redis) or vol-<name>.tar: "…Z-before-r3.dump" → "Postgres, before r3".
 */
export function backupLabel(name: string): { what: string; why: string } | null {
  const m = /Z-([a-z0-9-]+)\.(dump|rdb|vol-[a-z0-9-]+\.tar)$/.exec(name);
  if (!m) return null;
  const what = m[2] === 'dump' ? 'Postgres' : m[2] === 'rdb' ? 'Redis' : `Volume ${m[2].slice(4, -4)}`;
  return { what, why: m[1].replace('-', ' ') };
}

export type Tone = 'positive' | 'negative' | 'warning' | 'neutral';

export const releaseStatus: Record<ReleaseStatus, { word: string; tone: Tone }> = {
  live: { word: 'Live', tone: 'positive' },
  failed: { word: 'Failed', tone: 'negative' },
  superseded: { word: 'Previous', tone: 'neutral' },
  building: { word: 'Building', tone: 'warning' },
  releasing: { word: 'Release step', tone: 'warning' },
  starting: { word: 'Starting', tone: 'warning' },
};

export const serviceStatus: Record<Service['state'], { word: string; tone: Tone }> = {
  running: { word: 'Running', tone: 'positive' },
  restarting: { word: 'Restarting', tone: 'warning' },
  exited: { word: 'Exited', tone: 'negative' },
  missing: { word: 'Missing', tone: 'negative' },
};

/** Kayly's messages quote commands in backticks; this splits them into text and code. */
export function splitCode(text: string): { code: boolean; text: string }[] {
  return text
    .split('`')
    .map((part, i) => ({ code: i % 2 === 1, text: part }))
    .filter((p) => p.text !== '');
}
