import { fireEvent, render, screen } from "@testing-library/react"
import StatusCard from "../../src/components/StatusCard"
import formatTimestamp from "../../src/formatTimestamp"
import { StackStatus } from "../../src/hooks/useFetchStatuses"

describe("StatusCard", () => {
  const status: StackStatus = {
    name: "Some Name Here",
    error: "",
    revision: "3.76.1",
    repo_url: "https://www.github.com/1234",
    ref_type: "branch",
    ref_value: "main",
    compose_file: "docker-compose.yaml",
    last_change_at: null,
    last_deployed_at: null
  }

  function renderCard(overrides: Partial<StackStatus> = {}, onSelect: (name: string) => void = () => {}): void {
    render(<StatusCard status={{ ...status, ...overrides }} onSelect={onSelect} />)
  }

  it("should render name, revision, and repo_url properties", () => {
    renderCard()

    expect(screen.getByText(status.name)).toBeInTheDocument()
    expect(screen.getByText(status.revision)).toBeInTheDocument()
    expect(screen.getByText(status.repo_url)).toBeInTheDocument()
  })

  it("should render ref as 'type: value'", () => {
    renderCard()

    expect(screen.getByText("branch: main")).toBeInTheDocument()
  })

  it("should render repo_url as a link", () => {
    renderCard()

    const repoUrlElement = screen.getByRole("link", { name: status.repo_url })
    expect(repoUrlElement).toBeInTheDocument()
    expect(repoUrlElement).toHaveAttribute("href", status.repo_url)
  })

  it("should not render error if it is empty", () => {
    renderCard()

    const errorText = screen.queryByText(/error/i)
    expect(errorText).not.toBeInTheDocument()
  })

  it("should render error if it is not empty", () => {
    renderCard({ error: "Oh no!" })

    const errorText = screen.queryByText(/error/i)
    expect(errorText).toBeInTheDocument()
  })

  it("should not render timestamps when null", () => {
    renderCard()

    expect(screen.queryByText("Changed:")).not.toBeInTheDocument()
    expect(screen.queryByText("Deployed:")).not.toBeInTheDocument()
  })

  it("should render timestamps when provided", () => {
    const now = new Date()
    const twoHoursAgo = new Date(now.getTime() - 2 * 60 * 60 * 1000).toISOString()
    const fiveMinutesAgo = new Date(now.getTime() - 5 * 60 * 1000).toISOString()

    renderCard({ last_change_at: twoHoursAgo, last_deployed_at: fiveMinutesAgo })

    expect(screen.getByText("Changed:")).toBeInTheDocument()
    expect(screen.getByText("2h ago")).toBeInTheDocument()
    expect(screen.getByText("Deployed:")).toBeInTheDocument()
    expect(screen.getByText("5m ago")).toBeInTheDocument()
  })

  it("shows a healthy status icon when there is no error", () => {
    renderCard()
    expect(screen.getByLabelText("healthy")).toBeInTheDocument()
  })

  it("shows a degraded status icon when there is an error", () => {
    renderCard({ error: "boom" })
    expect(screen.getByLabelText("degraded")).toBeInTheDocument()
  })

  it("calls onSelect with the stack name when clicked", () => {
    const onSelect = vi.fn()
    renderCard({}, onSelect)

    fireEvent.click(screen.getByRole("button", { name: `View services graph for ${status.name}` }))
    expect(onSelect).toHaveBeenCalledWith(status.name)
  })

  it("does not call onSelect when the repo link is clicked", () => {
    const onSelect = vi.fn()
    renderCard({}, onSelect)

    fireEvent.click(screen.getByRole("link", { name: status.repo_url }))
    expect(onSelect).not.toHaveBeenCalled()
  })
})

describe("StatusCard update windows", () => {
  const base: StackStatus = {
    name: "media",
    error: "",
    revision: "abc",
    repo_url: "https://example.com/repo",
    ref_type: "branch",
    ref_value: "main",
    compose_file: "compose.yaml",
    last_change_at: null,
    last_deployed_at: null
  }
  const inTwoHours = new Date(Date.now() + 2 * 60 * 60 * 1000 + 30 * 1000).toISOString()

  it("shows a deferred badge and the next window for a deferred stack", () => {
    render(<StatusCard status={{ ...base, deferred_until: inTwoHours }} onSelect={() => {}} />)

    expect(screen.getByText("deferred")).toBeInTheDocument()
    expect(screen.getByText("in 2h")).toBeInTheDocument()
  })

  it("lists held back services", () => {
    render(
      <StatusCard
        status={{ ...base, deferred_services: { plex: inTwoHours, jellyfin: inTwoHours } }}
        onSelect={() => {}}
      />
    )

    expect(screen.getByText("jellyfin (in 2h), plex (in 2h)")).toBeInTheDocument()
    expect(screen.queryByText("deferred")).not.toBeInTheDocument()
  })
})

describe("formatTimestamp", () => {
  it("should return 'just now' for recent timestamps", () => {
    const now = new Date().toISOString()
    expect(formatTimestamp(now)).toBe("just now")
  })

  it("should return minutes ago", () => {
    const tenMinutesAgo = new Date(Date.now() - 10 * 60 * 1000).toISOString()
    expect(formatTimestamp(tenMinutesAgo)).toBe("10m ago")
  })

  it("should return hours ago", () => {
    const threeHoursAgo = new Date(Date.now() - 3 * 60 * 60 * 1000).toISOString()
    expect(formatTimestamp(threeHoursAgo)).toBe("3h ago")
  })

  it("should format future timestamps", () => {
    const inThreeHours = new Date(Date.now() + 3 * 60 * 60 * 1000 + 30 * 1000).toISOString()
    expect(formatTimestamp(inThreeHours)).toBe("in 3h")
  })

  it("should return days ago", () => {
    const twoDaysAgo = new Date(Date.now() - 2 * 24 * 60 * 60 * 1000).toISOString()
    expect(formatTimestamp(twoDaysAgo)).toBe("2d ago")
  })
})
