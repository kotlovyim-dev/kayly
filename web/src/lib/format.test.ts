import { describe, expect, it } from 'vitest';
import { backupLabel, formatBytes, relativeTime, shortCommit, splitCode } from './format';

describe('relativeTime', () => {
  const now = Date.parse('2026-10-20T12:00:00Z');
  it.each([
    ['2026-10-20T11:59:30Z', 'just now'],
    ['2026-10-20T11:22:00Z', '38 minutes ago'],
    ['2026-10-19T10:00:00Z', 'yesterday'],
    ['2026-10-13T12:00:00Z', '7 days ago'],
  ])('%s is %s', (at, want) => {
    expect(relativeTime(at, now)).toBe(want);
  });
});

describe('formatBytes', () => {
  it.each([
    [512, '512 B'],
    [2048, '2.0 KB'],
    [182 * 1024 ** 2, '182.0 MB'],
    [31.4 * 1024 ** 3, '31.4 GB'],
  ])('%d is %s', (n, want) => {
    expect(formatBytes(n)).toBe(want);
  });
});

describe('shortCommit', () => {
  it('keeps seven characters', () => expect(shortCommit('3a77de6f1c')).toBe('3a77de6'));
  it('marks a dirty work tree', () => expect(shortCommit('51fd0a3c2-dirty')).toBe('51fd0a3*'));
});

describe('backupLabel', () => {
  it.each([
    ['20261017T154007Z-before-r3.dump', { what: 'Postgres', why: 'before r3' }],
    ['20261020T030000Z-daily.rdb', { what: 'Redis', why: 'daily' }],
    ['20261020T030000Z-daily.vol-uploads.tar', { what: 'Volume uploads', why: 'daily' }],
  ])('%s', (name, want) => {
    expect(backupLabel(name)).toEqual(want);
  });
  it('gives up on a name it does not know', () => expect(backupLabel('kayly.tar')).toBeNull());
});

describe('splitCode', () => {
  it('separates quoted commands', () => {
    expect(splitCode('run `kayly env set KEY=…` first')).toEqual([
      { code: false, text: 'run ' },
      { code: true, text: 'kayly env set KEY=…' },
      { code: false, text: ' first' },
    ]);
  });
});
