package swarmcd

import (
	"fmt"
	"sync"
	"time"
)

var stackStatus = map[string]*StackStatus{}
var stacks []*swarmStack

// stateMu protects stackStatus and stacks from concurrent access.
// Lock ordering: stateMu must never be acquired while repo.lock is held.
// Acquiring stateMu independently (without any repo.lock) is always safe.
var stateMu sync.RWMutex

func Run() {
	logger.Info("starting SwarmCD")
	for {
		logger.Info("starting update loop")
		stateMu.RLock()
		currentStacks := make([]*swarmStack, len(stacks))
		copy(currentStacks, stacks)
		stateMu.RUnlock()

		var waitGroup sync.WaitGroup
		stacksChannel := make(chan *swarmStack, len(currentStacks))

		// Start worker pool
		logger.Debug(fmt.Sprintf("worker count: %d", config.Concurrency))
		for range config.Concurrency {
			go worker(stacksChannel, &waitGroup)
		}

		// Send stacks to workers
		for _, swarmStack := range currentStacks {
			logger.Debug(fmt.Sprintf("Queueing stack %s for update", swarmStack.name))
			waitGroup.Add(1)
			stacksChannel <- swarmStack
		}
		close(stacksChannel)

		// Wait for all workers to complete
		waitGroup.Wait()

		logger.Info("waiting for the update interval")
		time.Sleep(time.Duration(config.UpdateInterval) * time.Second)
	}
}

func worker(workerStacks <-chan *swarmStack, waitGroup *sync.WaitGroup) {
	for swarmStack := range workerStacks {
		updateStackThread(swarmStack)
		waitGroup.Done()
	}
}

func updateStackThread(swarmStack *swarmStack) {
	repoLock := swarmStack.repo.lock
	repoLock.Lock()
	logger.Debug(fmt.Sprintf("%s checking if stack needs to be updated", swarmStack.name))
	start := time.Now()
	result, err := swarmStack.updateStack()
	duration := time.Since(start)
	repoLock.Unlock() // Release repo.lock BEFORE acquiring stateMu

	now := time.Now()

	stateMu.Lock()
	status := stackStatus[swarmStack.name]
	if err != nil {
		status.Error = err.Error()
	} else {
		if result.Revision != "" && result.Revision != status.Revision {
			status.LastChangeAt = &now
		}
		status.Error = ""
		status.DeferredUntil = result.DeferredUntil
		status.DeferredServices = result.DeferredServices
		if result.Revision != "" {
			status.Revision = result.Revision
		}
		if result.DeferredUntil == nil {
			// updateStack runs `docker stack deploy` on every successful check
			status.LastDeployedAt = &now
		}
	}
	statusCopy := *status
	stateMu.Unlock()

	recordStackUpdate(swarmStack.name, statusCopy, result, err, duration)

	if err != nil {
		logger.Error(err.Error())
		return
	}
	logger.Info(fmt.Sprintf("%s stack updates check done", swarmStack.name))
}

// GetRuntimeInfo returns a copy of the instance's runtime metadata.
func GetRuntimeInfo() RuntimeInfo {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return runtimeInfo
}

// GetStackStatus returns a snapshot of all stack statuses under a read lock.
// Pointer and map fields are deep-copied so callers cannot mutate the original
// values; update the copy logic if more pointer, map or slice fields are added.
func GetStackStatus() map[string]*StackStatus {
	stateMu.RLock()
	defer stateMu.RUnlock()
	snapshot := make(map[string]*StackStatus, len(stackStatus))
	for k, v := range stackStatus {
		cp := *v
		cp.LastChangeAt = copyTime(v.LastChangeAt)
		cp.LastDeployedAt = copyTime(v.LastDeployedAt)
		cp.DeferredUntil = copyTime(v.DeferredUntil)
		if v.DeferredServices != nil {
			cp.DeferredServices = make(map[string]time.Time, len(v.DeferredServices))
			for service, t := range v.DeferredServices {
				cp.DeferredServices[service] = t
			}
		}
		snapshot[k] = &cp
	}
	return snapshot
}

func copyTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}
