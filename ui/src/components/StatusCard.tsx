import { Box, Flex, Grid, Link, Text, TextProps } from "@chakra-ui/react"
import React from "react"
import formatTimestamp from "../formatTimestamp"
import { StackStatus } from "../hooks/useFetchStatuses"
import { healthColor, healthIcon } from "./health"

function StatusCard({
  status,
  onSelect
}: Readonly<{
  status: StackStatus
  onSelect: (name: string) => void
}>): React.ReactElement {
  const { name, error, revision, repo_url, ref_type, ref_value, last_change_at, last_deployed_at } = status
  // Lightweight stack status icon from the reconcile error field (no extra
  // Docker calls on the dashboard list): error -> degraded, otherwise healthy.
  const level = error !== "" ? "degraded" : "healthy"
  const StatusIcon = healthIcon(level)
  const statusColor = healthColor(level)

  return (
    <Box
      borderWidth="1px"
      borderRadius="sm"
      overflow="hidden"
      p={4}
      mb={3}
      boxShadow="lg"
      cursor="pointer"
      transition="box-shadow 0.15s"
      _hover={{ boxShadow: "xl" }}
      role="button"
      tabIndex={0}
      aria-label={`View services graph for ${name}`}
      onClick={() => onSelect(name)}
      onKeyDown={e => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault()
          onSelect(name)
        }
      }}
    >
      <Flex justify="flex-end" align="center" gap={1} mb={1}>
        <Box as="span" color={statusColor} display="inline-flex" title={level} aria-label={level}>
          <StatusIcon />
        </Box>
        <Text fontSize="xs" color={statusColor} textTransform="capitalize">
          {level}
        </Text>
      </Flex>
      <Grid templateColumns="auto 1fr" gap={2}>
        <KeyText>Name:</KeyText>
        <Text>{name}</Text>

        {error !== "" && (
          <>
            <KeyText>Error:</KeyText>
            <Text color="red.500">{error}</Text>
          </>
        )}

        <KeyText>Watching:</KeyText>
        <Text>
          {ref_type}: {ref_value}
        </Text>

        <KeyText>Revision:</KeyText>
        <Text>{revision}</Text>

        <KeyText>Repo URL:</KeyText>
        <Link color="teal.500" href={repo_url} isExternal onClick={e => e.stopPropagation()}>
          {repo_url}
        </Link>

        {last_change_at && (
          <>
            <KeyText>Changed:</KeyText>
            <Text>{formatTimestamp(last_change_at)}</Text>
          </>
        )}

        {last_deployed_at && (
          <>
            <KeyText>Deployed:</KeyText>
            <Text>{formatTimestamp(last_deployed_at)}</Text>
          </>
        )}
      </Grid>
    </Box>
  )
}

function KeyText({ children, ...props }: Readonly<TextProps>): React.ReactElement {
  return (
    <Text fontWeight="bold" {...props}>
      {children}
    </Text>
  )
}

export default StatusCard
