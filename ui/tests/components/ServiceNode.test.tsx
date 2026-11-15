import { ChakraProvider } from "@chakra-ui/react"
import { render, screen } from "@testing-library/react"
import type { NodeProps } from "@xyflow/react"
import ServiceNode, { type ServiceFlowNode } from "../../src/components/ServiceNode"
import { ServiceStatus } from "../../src/hooks/useFetchStackServices"

// React Flow's Handle needs a provider context; stub it out for unit testing
// the node's presentation.
vi.mock("@xyflow/react", () => ({
  Handle: () => null,
  Position: { Left: "left", Right: "right", Top: "top", Bottom: "bottom" }
}))

const baseService: ServiceStatus = {
  id: "svc-1",
  name: "web_nginx",
  image: "nginx:1.27",
  mode: "replicated",
  running_tasks: 3,
  desired_tasks: 3,
  failed_tasks: 0,
  health: "healthy",
  tasks: [],
  secrets: [],
  configs: []
}

function nodeProps(service: ServiceStatus): NodeProps<ServiceFlowNode> {
  return { data: { service } } as unknown as NodeProps<ServiceFlowNode>
}

describe("ServiceNode", () => {
  it("renders service name, image, mode and replica counts", () => {
    render(
      <ChakraProvider>
        <ServiceNode {...nodeProps(baseService)} />
      </ChakraProvider>
    )

    expect(screen.getByText("web_nginx")).toBeInTheDocument()
    expect(screen.getByText("nginx:1.27")).toBeInTheDocument()
    expect(screen.getByText("replicated")).toBeInTheDocument()
    expect(screen.getByText("3/3 replicas")).toBeInTheDocument()
  })

  it.each(["healthy", "progressing", "degraded"])("exposes the %s health via the indicator title", health => {
    render(
      <ChakraProvider>
        <ServiceNode {...nodeProps({ ...baseService, health: health })} />
      </ChakraProvider>
    )

    expect(screen.getByTitle(health)).toBeInTheDocument()
  })

  it("shows the failed-task count when failed_tasks > 0", () => {
    render(
      <ChakraProvider>
        <ServiceNode {...nodeProps({ ...baseService, failed_tasks: 2 })} />
      </ChakraProvider>
    )

    expect(screen.getByText(/2 failed/)).toBeInTheDocument()
  })

  it("shows a held back badge when the service update window is closed", () => {
    const inTwoHours = new Date(Date.now() + 2 * 60 * 60 * 1000 + 30 * 1000).toISOString()
    render(
      <ChakraProvider>
        <ServiceNode {...nodeProps({ ...baseService, deferred_until: inTwoHours })} />
      </ChakraProvider>
    )

    expect(screen.getByText("held back · in 2h")).toBeInTheDocument()
  })

  it("shows no held back badge otherwise", () => {
    render(
      <ChakraProvider>
        <ServiceNode {...nodeProps(baseService)} />
      </ChakraProvider>
    )

    expect(screen.queryByText(/held back/)).not.toBeInTheDocument()
  })
})
