import { createRoot } from 'react-dom/client'
import { DesktopRoot } from './desktop/DesktopRoot'
import { LocaleProvider } from './locale'
import { ThemeProvider } from './layout/Theme'
import '@fontsource-variable/inter/wght.css'
import './styles/app.css'

const root = createRoot(document.getElementById('root')!)
void window.desktop.configuration().then(configuration => {
  root.render(<LocaleProvider initial={configuration.language}><ThemeProvider><DesktopRoot /></ThemeProvider></LocaleProvider>)
}, () => {
  root.render(<LocaleProvider><ThemeProvider><DesktopRoot /></ThemeProvider></LocaleProvider>)
})
