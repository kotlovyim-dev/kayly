import { expect, it } from 'vitest';
import { cn } from './utils';

it('keeps a text colour next to a theme font size', () => {
  expect(cn('text-primary-foreground', 'text-body')).toBe('text-primary-foreground text-body');
});

it('lets a later font size win', () => {
  expect(cn('text-body', 'text-small')).toBe('text-small');
});
