import { createRoot } from 'react-dom/client'
import { DesktopRoot } from './desktop/DesktopRoot'
import { LocaleProvider } from './locale'
import { ThemeProvider } from './layout/Theme'
import '@fontsource-variable/inter/wght.css'
import './styles/app.css'

createRoot(document.getElementById('root')!).render(<LocaleProvider><ThemeProvider><DesktopRoot /></ThemeProvider></LocaleProvider>)
