import { Text } from "@chakra-ui/react"
import React, { useEffect, useState } from "react"
import { StackStatus } from "../hooks/useFetchStatuses"
import StatusCard from "./StatusCard"

function StatusCardList({
  statuses,
  query,
  onSelect
}: Readonly<{ statuses: StackStatus[]; query: string; onSelect: (name: string) => void }>): React.ReactElement {
  const [filteredStatuses, setFilteredStatuses] = useState<StackStatus[]>(statuses)

  useEffect(() => {
    const filtered = statuses.filter(status =>
      Object.values(status).some(value => value != null && value.toString().toLowerCase().includes(query.toLowerCase()))
    )
    setFilteredStatuses(filtered)
  }, [statuses, query])

  return (
    <>
      {filteredStatuses.length === 0 ? (
        <Text fontSize="xl" align="center" mt={4}>
          No items available
        </Text>
      ) : (
        filteredStatuses.map(item => <StatusCard key={item.name} status={item} onSelect={onSelect} />)
      )}
    </>
  )
}

export default StatusCardList
