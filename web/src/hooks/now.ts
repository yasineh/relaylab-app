import { useEffect, useState } from 'react'

/**
 * The current time, re-read every `intervalMs`, for relative timestamps
 * ("2 minutes ago") that stay correct while a page is open.
 */
export function useNow(intervalMs = 30_000): Date {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}
