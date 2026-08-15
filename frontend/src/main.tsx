import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import './styles.css'
import { NabuProvider } from './state/NabuProvider'
import { FileViewerProvider } from './components/FileViewer'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <NabuProvider>
        <FileViewerProvider><App /></FileViewerProvider>
      </NabuProvider>
    </BrowserRouter>
  </StrictMode>,
)
