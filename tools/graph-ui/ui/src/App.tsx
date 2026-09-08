import { Routes, Route, Navigate } from 'react-router-dom'
import Layout from './components/Layout'
import ExplorerPage from './pages/ExplorerPage'

export default function App() {
  return (
    <Layout>
      <Routes>
        <Route path="/" element={<Navigate to="/explorer" replace />} />
        <Route path="/explorer" element={<ExplorerPage />} />
      </Routes>
    </Layout>
  )
}
