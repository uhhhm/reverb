// @ts-expect-error - CSS-only package without types
import '@fontsource-variable/figtree'
import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
import App from './App'
import { getSettings, applyAccent } from './lib/settingsApi'
import { applyTheme, applyThemeFromCache } from './lib/themes'
import './index.css'

// Best-effort: theme the app with the saved accent + theme before the user notices.
// 1) Apply cached theme synchronously to avoid flash.
// 2) Fetch authoritative settings and apply both accent and theme.
applyThemeFromCache()
void getSettings()
  .then((s) => {
    applyAccent(s.accentColor)
    if (s.theme) applyTheme(s.theme)
  })
  .catch(() => {})

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <Routes>
        <Route path="/*" element={<App />} />
      </Routes>
    </BrowserRouter>
  </React.StrictMode>,
)
