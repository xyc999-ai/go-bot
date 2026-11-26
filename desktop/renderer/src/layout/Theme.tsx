import { createContext, useContext, useLayoutEffect, useState, type ReactNode } from 'react'
import { Moon, Sun } from 'lucide-react'
import { useLocale } from '../locale'

type Theme = 'light' | 'dark'
const Context = createContext<{ theme: Theme; toggle: () => void } | undefined>(undefined)
function initial(): Theme {
  try { return localStorage.getItem('go-bot.desktop-theme') === 'dark' ? 'dark' : 'light' } catch { return 'light' }
}
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState(initial)
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = theme
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', theme === 'dark' ? '#141416' : '#ffffff')
    try { localStorage.setItem('go-bot.desktop-theme', theme) } catch { /* Storage can be disabled. */ }
  }, [theme])
  return <Context.Provider value={{ theme, toggle: () => setTheme(value => value === 'light' ? 'dark' : 'light') }}>{children}</Context.Provider>
}
export function ThemeToggle() {
  const { t } = useLocale()
  const value = useContext(Context)!
  const label = t(value.theme === 'light' ? 'Switch to dark theme' : 'Switch to light theme')
  return <button type="button" className="icon-button theme-toggle" aria-label={label} title={label} onClick={value.toggle}>
    {value.theme === 'light' ? <Moon size={18} /> : <Sun size={18} />}
  </button>
}
