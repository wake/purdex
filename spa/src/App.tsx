import SessionPanel from './components/SessionPanel'

export default function App() {
  return (
    <div className="h-screen bg-gray-950 text-gray-200 flex">
      <SessionPanel />
      <div className="flex-1 flex items-center justify-center">
        <p className="text-gray-500">Select a session</p>
      </div>
    </div>
  )
}
