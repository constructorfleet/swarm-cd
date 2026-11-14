package web

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/m-adawi/swarm-cd/swarmcd"
	"github.com/m-adawi/swarm-cd/util"
)

// getStackServicesFn is a seam over swarmcd.GetStackServices so the controller
// can be unit-tested without a live Docker daemon.
var getStackServicesFn = swarmcd.GetStackServices

type stackResponse struct {
	Name           string     `json:"name"`
	RepoURL        string     `json:"repo_url"`
	RefType        string     `json:"ref_type"`
	RefValue       string     `json:"ref_value"`
	Revision       string     `json:"revision"`
	ComposeFile    string     `json:"compose_file"`
	Error          string     `json:"error"`
	LastChangeAt   *time.Time `json:"last_change_at"`
	LastDeployedAt *time.Time `json:"last_deployed_at"`
}

// newStackResponse is the single place a StackStatus is mapped to its JSON
// representation, shared by every endpoint that returns stacks.
func newStackResponse(name string, status *swarmcd.StackStatus) stackResponse {
	return stackResponse{
		Name:           name,
		RepoURL:        status.RepoURL,
		RefType:        status.RefType,
		RefValue:       status.RefValue,
		Revision:       status.Revision,
		ComposeFile:    status.ComposeFile,
		Error:          status.Error,
		LastChangeAt:   status.LastChangeAt,
		LastDeployedAt: status.LastDeployedAt,
	}
}

func getHealth(ctx *gin.Context) {
	info := swarmcd.GetRuntimeInfo()
	stacksStatus := swarmcd.GetStackStatus()

	resp := gin.H{
		"status":                  "healthy",
		"booted_at":               info.BootedAt,
		"version":                 info.Version,
		"uptime_seconds":          math.Floor(time.Since(info.BootedAt).Seconds()),
		"update_interval_seconds": util.Configs.UpdateInterval,
		"stacks_managed":          len(stacksStatus),
	}
	if warnings := util.ConfigWarnings(); len(warnings) > 0 {
		resp["config_warnings"] = warnings
	}
	ctx.JSON(http.StatusOK, resp)
}

func getStack(ctx *gin.Context) {
	name := ctx.Param("name")
	status, ok := swarmcd.GetStackStatus()[name]
	if !ok {
		ctx.JSON(http.StatusNotFound, gin.H{
			"error": fmt.Sprintf("stack '%s' not found", name),
		})
		return
	}
	ctx.JSON(http.StatusOK, newStackResponse(name, status))
}

func getStacks(ctx *gin.Context) {
	stacksStatus := swarmcd.GetStackStatus()
	stacks := make([]stackResponse, 0, len(stacksStatus))
	for name, status := range stacksStatus {
		stacks = append(stacks, newStackResponse(name, status))
	}
	sort.Slice(stacks, func(i, j int) bool {
		return stacks[i].Name < stacks[j].Name
	})
	ctx.JSON(http.StatusOK, stacks)
}

func getStackServices(ctx *gin.Context) {
	name := ctx.Param("name")
	services, err := getStackServicesFn(name)
	if err != nil {
		if errors.Is(err, swarmcd.ErrStackNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{
				"error": fmt.Sprintf("stack '%s' not found", name),
			})
			return
		}
		util.Logger.Error("failed to get stack services", "stack", name, "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "could not list services"})
		return
	}
	ctx.JSON(http.StatusOK, services)
}
