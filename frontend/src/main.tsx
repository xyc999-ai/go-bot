import { createRoot } from 'react-dom/client'
import { App } from './App'
import { LocaleProvider } from './locale'
import { ThemeProvider } from './layout/Theme'
import '@fontsource-variable/inter/wght.css'
import './styles/app.css'

createRoot(document.getElementById('root')!).render(<LocaleProvider><ThemeProvider><App /></ThemeProvider></LocaleProvider>)
