import React from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
import RealTest from './pages/RealTest'

function App() {
  return (
    <BrowserRouter>
      <div>
        <Routes>
          <Route path="*" element={<RealTest/>} />
        </Routes>
      </div>
    </BrowserRouter>
  )
}

createRoot(document.getElementById('root')!).render(<App />)
