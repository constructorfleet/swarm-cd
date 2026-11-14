import { ChakraProvider } from "@chakra-ui/react"
import { render, screen } from "@testing-library/react"
import type { NodeProps } from "@xyflow/react"
import TaskNode, { type TaskFlowNode } from "../../src/components/TaskNode"
import { TaskStatus } from "../../src/hooks/useFetchStackServices"

vi.mock("@xyflow/react", () => ({
  Handle: () => null,
  Position: { Left: "left", Right: "right", Top: "top", Bottom: "bottom" }
}))

function nodeProps(task: TaskStatus): NodeProps<TaskFlowNode> {
  return { data: { task } } as unknown as NodeProps<TaskFlowNode>
}

const runningTask: TaskStatus = {
  id: "t-1",
  slot: 1,
  node_id: "node-1",
  state: "running",
  desired_state: "running",
  error: "",
  container_id: "c0ffee"
}

describe("TaskNode", () => {
  it("renders slot label and running state", () => {
    render(
      <ChakraProvider>
        <TaskNode {...nodeProps(runningTask)} />
      </ChakraProvider>
    )

    expect(screen.getByText("Slot 1")).toBeInTheDocument()
    expect(screen.getByTitle("running")).toBeInTheDocument()
  })

  it("renders the error text for a failed task", () => {
    render(
      <ChakraProvider>
        <TaskNode {...nodeProps({ ...runningTask, state: "failed", error: "task: non-zero exit (1)" })} />
      </ChakraProvider>
    )

    expect(screen.getByText("task: non-zero exit (1)")).toBeInTheDocument()
    expect(screen.getByTitle("failed")).toBeInTheDocument()
  })
})
