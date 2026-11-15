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
	result, err := swarmStack.updateStack()
	repoLock.Unlock() // Release repo.lock BEFORE acquiring stateMu

	stateMu.Lock()
	status := stackStatus[swarmStack.name]
	if err != nil {
		status.Error = err.Error()
	} else {
		status.Error = ""
		status.DeferredUntil = result.DeferredUntil
		status.DeferredServices = result.DeferredServices
		if result.Revision != "" {
			status.Revision = result.Revision
		}
	}
	stateMu.Unlock()

	if err != nil {
		logger.Error(err.Error())
		return
	}
	logger.Info(fmt.Sprintf("%s stack updates check done", swarmStack.name))
}

// GetStackStatus returns a snapshot of all stack statuses under a read lock.
// The returned map is a deep copy — callers cannot mutate the original values.
func GetStackStatus() map[string]*StackStatus {
	stateMu.RLock()
	defer stateMu.RUnlock()
	snapshot := make(map[string]*StackStatus, len(stackStatus))
	for k, v := range stackStatus {
		cp := *v
		if v.DeferredUntil != nil {
			t := *v.DeferredUntil
			cp.DeferredUntil = &t
		}
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
