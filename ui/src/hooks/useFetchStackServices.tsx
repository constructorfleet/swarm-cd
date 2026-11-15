import { useEffect, useState } from "react"
import devServices from "./dummyStackServices.json"

export interface TaskStatus {
  id: string
  slot: number
  node_id: string
  state: string
  desired_state: string
  error: string
  container_id: string
}

export interface ServiceStatus {
  id: string
  name: string
  image: string
  mode: string
  running_tasks: number
  desired_tasks: number
  failed_tasks: number
  health: string
  tasks: TaskStatus[]
  secrets: string[]
  configs: string[]
  // set while the service is held back because its update window is closed
  deferred_until?: string | null
}

// Dev-mode fixture keyed by stack name (see dummyStackServices.json). The
// explicit type both documents the contract and fails the build if the JSON
// drifts from the ServiceStatus shape.
const dummyServicesByStack: Record<string, ServiceStatus[]> = devServices

async function fetchFromServer(stackName: string): Promise<ServiceStatus[]> {
  const response = await fetch(`/stacks/${encodeURIComponent(stackName)}/services`)
  if (!response.ok) {
    throw new Error("Network response was not ok")
  }

  return (await response.json()) as ServiceStatus[]
}

export default function useFetchStackServices(
  stackName: string | null,
  intervalMs = 5000
): { services: ServiceStatus[]; error: string | null; loading: boolean } {
  const [services, setServices] = useState<ServiceStatus[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (stackName === null) {
      setServices([])
      setError(null)
      setLoading(false)
      return
    }

    let cancelled = false

    const fetchServices = async (): Promise<void> => {
      try {
        const data =
          import.meta.env.MODE === "development"
            ? (dummyServicesByStack[stackName] ?? [])
            : await fetchFromServer(stackName)
        if (!cancelled) {
          setServices(data)
          setError(null)
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "An unknown error occurred")
        }
      } finally {
        if (!cancelled) {
          setLoading(false)
        }
      }
    }

    setLoading(true)
    void fetchServices() // initial fetch

    const intervalId = setInterval(() => {
      void fetchServices()
    }, intervalMs)

    return () => {
      cancelled = true
      clearInterval(intervalId)
    }
  }, [stackName, intervalMs])

  return { services, error, loading }
}
