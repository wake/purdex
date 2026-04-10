import { hostFetch } from './host-api'

export async function executeCommand(hostId: string, sessionCode: string, command: string): Promise<void> {
  const res = await hostFetch(hostId, `/api/sessions/${sessionCode}/send-keys`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ keys: command + '\n' }),
  })
  if (!res.ok) throw new Error(`send-keys failed: ${res.status}`)
}
