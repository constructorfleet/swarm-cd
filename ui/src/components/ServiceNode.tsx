import { Badge, Box, Flex, Text, useColorModeValue } from "@chakra-ui/react"
import { Handle, Position, type Node, type NodeProps } from "@xyflow/react"
import React from "react"
import formatTimestamp from "../formatTimestamp"
import { ServiceStatus } from "../hooks/useFetchStackServices"
import { healthColor, healthIcon } from "./health"

export type ServiceNodeData = {
  service: ServiceStatus
}

export type ServiceFlowNode = Node<ServiceNodeData, "service">

function ServiceNode({ data }: NodeProps<ServiceFlowNode>): React.ReactElement {
  const { service } = data
  const bg = useColorModeValue("white", "gray.700")
  const color = healthColor(service.health)
  const HealthIcon = healthIcon(service.health)

  return (
    <Box
      bg={bg}
      borderWidth="1px"
      borderRadius="md"
      borderLeftWidth="4px"
      borderLeftColor={color}
      boxShadow="md"
      px={3}
      py={2}
      minW="210px"
    >
      <Handle type="target" position={Position.Left} />
      <Handle type="source" position={Position.Right} />
      <Flex align="center" gap={2}>
        <Box
          as="span"
          title={service.health}
          aria-label={service.health}
          color={color}
          display="inline-flex"
          fontSize="md"
        >
          <HealthIcon />
        </Box>
        <Text fontWeight="bold" fontSize="sm" noOfLines={1}>
          {service.name}
        </Text>
      </Flex>
      <Text fontSize="xs" color="gray.500" noOfLines={1} mt={1}>
        {service.image}
      </Text>
      <Flex align="center" gap={2} mt={1} wrap="wrap">
        <Badge textTransform="lowercase">{service.mode}</Badge>
        <Text fontSize="xs">
          {service.running_tasks}/{service.desired_tasks} replicas
        </Text>
        {service.failed_tasks > 0 && (
          <Text fontSize="xs" color="red.500" fontWeight="semibold">
            {service.failed_tasks} failed
          </Text>
        )}
        {service.deferred_until && (
          <Badge
            colorScheme="purple"
            textTransform="lowercase"
            title={`Update window opens ${new Date(service.deferred_until).toLocaleString()}`}
          >
            held back · {formatTimestamp(service.deferred_until)}
          </Badge>
        )}
      </Flex>
    </Box>
  )
}

export default ServiceNode
