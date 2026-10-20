import { useEffect, useState } from 'react';

export type Theme = 'system' | 'light' | 'dark';

const key = 'kayly-theme';

function stored(): Theme {
  try {
    const v = localStorage.getItem(key);
    return v === 'light' || v === 'dark' ? v : 'system';
  } catch {
    return 'system';
  }
}

/** The colour theme: the system's unless the owner picked one, remembered in this browser. */
export function useTheme(): [Theme, (t: Theme) => void] {
  const [theme, setTheme] = useState<Theme>(stored);
  useEffect(() => {
    const root = document.documentElement;
    if (theme === 'system') root.removeAttribute('data-theme');
    else root.setAttribute('data-theme', theme);
    try {
      if (theme === 'system') localStorage.removeItem(key);
      else localStorage.setItem(key, theme);
    } catch {
      // private window: the choice lasts for this page only
    }
  }, [theme]);
  return [theme, setTheme];
}
