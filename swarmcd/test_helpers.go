// test_helpers.go lives in the swarmcd package (not a _test.go file) so that
// test files in other packages (e.g., web/) can call these helpers to safely
// swap internal state. This is intentional — SwarmCD is a standalone binary,
// not a library, so the exported surface area is not a concern.
package swarmcd

// SetStackStatusForTest replaces the internal stackStatus map with the given
// test data and returns a cleanup function that restores the original state.
// This is intended for use in tests only.
func SetStackStatusForTest(testStatus map[string]*StackStatus) func() {
	stateMu.Lock()
	oldStatus := stackStatus
	oldStacks := stacks
	stackStatus = testStatus
	stacks = nil
	stateMu.Unlock()

	return func() {
		stateMu.Lock()
		stackStatus = oldStatus
		stacks = oldStacks
		stateMu.Unlock()
	}
}

// SetRuntimeInfoForTest replaces the internal runtimeInfo with the given
// test data and returns a cleanup function that restores the original state.
// This is intended for use in tests only.
func SetRuntimeInfoForTest(info RuntimeInfo) func() {
	stateMu.Lock()
	old := runtimeInfo
	runtimeInfo = info
	stateMu.Unlock()
	return func() {
		stateMu.Lock()
		runtimeInfo = old
		stateMu.Unlock()
	}
}
