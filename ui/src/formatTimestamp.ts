// formatTimestamp renders an ISO timestamp relative to now: "5m ago" for past
// times and "in 5m" for future ones (e.g. when an update window opens).
export default function formatTimestamp(iso: string): string {
  const delta = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  const future = delta < 0
  const seconds = Math.abs(delta)
  if (seconds < 60) return future ? "in under a minute" : "just now"
  const minutes = Math.floor(seconds / 60)
  const hours = Math.floor(minutes / 60)
  const days = Math.floor(hours / 24)
  let amount: string
  if (minutes < 60) amount = `${minutes}m`
  else if (hours < 24) amount = `${hours}h`
  else amount = `${days}d`
  return future ? `in ${amount}` : `${amount} ago`
}
