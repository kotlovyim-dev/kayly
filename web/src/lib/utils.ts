import { type ClassValue, clsx } from 'clsx';
import { extendTailwindMerge } from 'tailwind-merge';

// The theme's own font sizes (styles.css); unknown, they would be merged away as text colours.
const twMerge = extendTailwindMerge({
  extend: { classGroups: { 'font-size': [{ text: ['small', 'body', 'section', 'title', 'figure'] }] } },
});

/** Joins class names, letting later Tailwind utilities override earlier ones. */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
